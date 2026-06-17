# Slice 1 — Controller + dynamic registration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Clicking the lobby compass spawns/joins a real, dynamically-spawned minigame pod, with a disposable warm-pool lifecycle (join → game ends → pod killed → pool refilled).

**Architecture:** A Go **controller** pod owns a warm pool of bare minigame Pods (k8s REST via the in-cluster ServiceAccount token, no client-go) and exposes a REST API. A tiny **velocity-register** plugin baked into the Velocity image registers/unregisters backends live over HTTP. The **lobby** plugin allocates an instance from the controller and sends the player via BungeeCord `Connect`. A single **minigame-stub** image is the placeholder game that can end itself via `/endgame` → `POST /done`.

**Tech Stack:** Go (stdlib only); Java 21 Paper plugins (lobby, stub-game) and a Velocity-API plugin (velocity-register), built with dockerized Gradle; k8s on local `kind`; `make`-driven registry-free image loop.

## Global Constraints

- Go module is `mc-platform`, `go 1.26.4`. **Stdlib only** — no new Go dependencies (no client-go).
- All k8s resources in namespace `mc`. Images tagged `mc/<name>:dev`.
- Minecraft `1.21.8`; Paper API `1.21.8-R0.1-SNAPSHOT`; Java toolchain 21. Velocity `3.4.0-SNAPSHOT`.
- Forwarding secret is the k8s Secret `velocity-forwarding`, mounted at `/secret/forwarding.secret`, never baked into an image. Backends fail-fast if it is missing.
- Controller↔velocity-register auth: bearer token from k8s Secret `controller-token`. Trust boundary — checked on every request.
- Base images: `useradd -r mc` (no fixed UID 1000); `ADD <url>` jars need `chmod 0644`; Velocity needs an empty `[forced-hosts]`; Gradle 9 needs `testRuntimeOnly junit-platform-launcher`.
- Mark deliberate shortcuts with a `ponytail:` comment naming what's skipped and when to add it.
- Non-trivial logic leaves one runnable check (assert-based Go test or JUnit).
- Build images with buildkit; `kind load docker-image` after each build (no registry).

---

### Task 1: Controller pool logic (pure core)

**Files:**
- Create: `controller/pool.go`
- Test: `controller/pool_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Pod struct { Name, Game, IP string; Ready, Alloc bool }`; `func needed(pods []Pod, desired int) int`; `func pickAllocatable(pods []Pod, game string) *Pod`.

Deletes are event-driven (a pod's own `/done` call), so reconcile only ever *creates* to refill — `needed` returns a count, not a delete list.

- [ ] **Step 1: Write the failing test**

```go
package main

import "testing"

func TestNeeded(t *testing.T) {
	cases := []struct {
		name    string
		pods    []Pod
		desired int
		want    int
	}{
		{"empty pool", nil, 2, 2},
		{"one free counts", []Pod{{Name: "a", Alloc: false}}, 2, 1},
		{"allocated does not count", []Pod{{Name: "a", Alloc: true}}, 1, 1},
		{"booting counts toward pool", []Pod{{Name: "a", Ready: false, Alloc: false}}, 1, 0},
		{"full pool", []Pod{{Name: "a"}, {Name: "b"}}, 2, 0},
		{"over-full clamps to zero", []Pod{{Name: "a"}, {Name: "b"}, {Name: "c"}}, 2, 0},
	}
	for _, c := range cases {
		if got := needed(c.pods, c.desired); got != c.want {
			t.Errorf("%s: needed=%d want %d", c.name, got, c.want)
		}
	}
}

func TestPickAllocatable(t *testing.T) {
	pods := []Pod{
		{Name: "booting", Game: "stub", Ready: false, Alloc: false},
		{Name: "taken", Game: "stub", Ready: true, Alloc: true},
		{Name: "ready", Game: "stub", Ready: true, Alloc: false, IP: "1.2.3.4"},
		{Name: "otherwise", Game: "other", Ready: true, Alloc: false},
	}
	got := pickAllocatable(pods, "stub")
	if got == nil || got.Name != "ready" {
		t.Fatalf("got %v, want pod 'ready'", got)
	}
	if pickAllocatable(pods, "nope") != nil {
		t.Error("expected nil for unknown game")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./controller/ -run 'TestNeeded|TestPickAllocatable' -v`
Expected: FAIL — `undefined: needed`, `undefined: pickAllocatable`, `undefined: Pod`.

- [ ] **Step 3: Write minimal implementation**

```go
package main

// Pod is the controller's view of a minigame pod (subset of k8s pod state).
type Pod struct {
	Name, Game, IP string
	Ready, Alloc   bool
}

// needed returns how many new pods to create to keep `desired` unallocated pods
// (Ready or still booting) in the pool. Allocated pods don't count — they belong
// to a game in progress. Deletes are not handled here; a finished pod removes
// itself via POST /done.
func needed(pods []Pod, desired int) int {
	free := 0
	for _, p := range pods {
		if !p.Alloc {
			free++
		}
	}
	if free >= desired {
		return 0
	}
	return desired - free
}

// pickAllocatable returns the first Ready, unallocated pod of the given game, or nil.
func pickAllocatable(pods []Pod, game string) *Pod {
	for i := range pods {
		p := &pods[i]
		if p.Game == game && p.Ready && !p.Alloc {
			return p
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./controller/ -run 'TestNeeded|TestPickAllocatable' -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add controller/pool.go controller/pool_test.go
git commit -m "feat(controller): pure pool logic (needed + pickAllocatable)"
```

---

### Task 2: Controller pod manifest builder + k8s REST client

**Files:**
- Create: `controller/k8s.go`
- Test: `controller/k8s_test.go`

**Interfaces:**
- Consumes: `Pod` (Task 1).
- Produces: `func podManifest(name, game, image, controllerURL string) string`; `type kube struct{...}`; `func newKube() (*kube, error)`; methods `(*kube) createPod(name, game, image, controllerURL string) error`, `deletePod(name string) error`, `setAllocated(name string) error`. (`listPods`/`parsePodList` are Task 3.)

Only `podManifest` is unit-tested (pure). The HTTP methods are exercised end-to-end in Task 12.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPodManifest(t *testing.T) {
	s := podManifest("mg-stub-abc", "stub", "mc/minigame-stub:dev", "http://controller.mc.svc.cluster.local:8080")
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	for _, want := range []string{"mg-stub-abc", "app", "minigame", "alloc", "mc/minigame-stub:dev", "INSTANCE_ID", "CONTROLLER_URL", "forwarding.secret"} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./controller/ -run TestPodManifest -v`
Expected: FAIL — `undefined: podManifest`.

- [ ] **Step 3: Write minimal implementation**

```go
package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const namespace = "mc"

// podManifest renders a bare minigame Pod as JSON. The pod runs the stub image,
// mounts the forwarding secret (it's an ASP backend), and learns its own identity
// + the controller URL from env so it can POST /done when its game ends.
// ponytail: JSON template string, not a typed PodSpec — no client-go, and the
// shape is fixed. Build a struct only if this manifest starts to branch.
func podManifest(name, game, image, controllerURL string) string {
	return fmt.Sprintf(`{
  "apiVersion":"v1","kind":"Pod",
  "metadata":{"name":%q,"namespace":%q,"labels":{"app":"minigame","game":%q,"alloc":"false"}},
  "spec":{
    "containers":[{
      "name":"minigame","image":%q,"imagePullPolicy":"IfNotPresent",
      "ports":[{"containerPort":25565}],
      "env":[{"name":"INSTANCE_ID","value":%q},{"name":"CONTROLLER_URL","value":%q}],
      "volumeMounts":[{"name":"secret","mountPath":"/secret","readOnly":true}],
      "readinessProbe":{"tcpSocket":{"port":25565},"initialDelaySeconds":20,"periodSeconds":5}
    }],
    "volumes":[{"name":"secret","secret":{"secretName":"velocity-forwarding","items":[{"key":"forwarding.secret","path":"forwarding.secret"}]}}]
  }
}`, name, namespace, game, image, name, controllerURL)
}

// kube is a minimal in-cluster k8s REST client. No client-go.
type kube struct {
	host, token string
	hc          *http.Client
}

func newKube() (*kube, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" {
		return nil, fmt.Errorf("not running in-cluster: KUBERNETES_SERVICE_HOST unset")
	}
	const base = "/var/run/secrets/kubernetes.io/serviceaccount"
	token, err := os.ReadFile(base + "/token")
	if err != nil {
		return nil, fmt.Errorf("read SA token: %w", err)
	}
	ca, err := os.ReadFile(base + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("read CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("bad CA cert")
	}
	return &kube{
		host:  fmt.Sprintf("https://%s:%s", host, port),
		token: strings.TrimSpace(string(token)),
		hc: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
		},
	}, nil
}

func (k *kube) do(method, path, contentType, body string) ([]byte, int, error) {
	req, err := http.NewRequest(method, k.host+path, strings.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+k.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := k.hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode, nil
}

func (k *kube) createPod(name, game, image, controllerURL string) error {
	b, code, err := k.do("POST", "/api/v1/namespaces/"+namespace+"/pods", "application/json", podManifest(name, game, image, controllerURL))
	if err != nil {
		return err
	}
	if code != 201 && code != 200 {
		return fmt.Errorf("createPod %s: %d %s", name, code, b)
	}
	return nil
}

func (k *kube) deletePod(name string) error {
	_, code, err := k.do("DELETE", "/api/v1/namespaces/"+namespace+"/pods/"+name, "", "")
	if err != nil {
		return err
	}
	if code != 200 && code != 202 && code != 404 {
		return fmt.Errorf("deletePod %s: %d", name, code)
	}
	return nil
}

func (k *kube) setAllocated(name string) error {
	patch := `{"metadata":{"labels":{"alloc":"true"}}}`
	_, code, err := k.do("PATCH", "/api/v1/namespaces/"+namespace+"/pods/"+name, "application/merge-patch+json", patch)
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("setAllocated %s: %d", name, code)
	}
	return nil
}
```

> Note: `listPods` is intentionally NOT in this file yet — it depends on `parsePodList`, which Task 3 adds. The package compiles fine without it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./controller/ -run TestPodManifest -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add controller/k8s.go controller/k8s_test.go
git commit -m "feat(controller): pod manifest builder + in-cluster k8s REST client"
```

---

### Task 3: Pod-list parsing + listPods

**Files:**
- Modify: `controller/k8s.go` (add `parsePodList` and `listPods`)
- Test: `controller/parse_test.go`

**Interfaces:**
- Consumes: `Pod` (Task 1); `(*kube).do` (Task 2).
- Produces: `func parsePodList(body []byte) ([]Pod, error)` — maps a k8s PodList JSON to `[]Pod` (Ready condition → `Ready`, label `alloc` → `Alloc`, `status.podIP` → `IP`); method `(*kube) listPods(game string) ([]Pod, error)`.

- [ ] **Step 1: Write the failing test**

```go
package main

import "testing"

func TestParsePodList(t *testing.T) {
	body := []byte(`{"items":[
	  {"metadata":{"name":"mg-stub-ready","labels":{"game":"stub","alloc":"false"}},
	   "status":{"podIP":"10.0.0.5","conditions":[{"type":"Ready","status":"True"}]}},
	  {"metadata":{"name":"mg-stub-booting","labels":{"game":"stub","alloc":"false"}},
	   "status":{"conditions":[{"type":"Ready","status":"False"}]}},
	  {"metadata":{"name":"mg-stub-taken","labels":{"game":"stub","alloc":"true"}},
	   "status":{"podIP":"10.0.0.6","conditions":[{"type":"Ready","status":"True"}]}}
	]}`)
	pods, err := parsePodList(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 3 {
		t.Fatalf("got %d pods", len(pods))
	}
	if !pods[0].Ready || pods[0].Alloc || pods[0].IP != "10.0.0.5" || pods[0].Game != "stub" {
		t.Errorf("ready pod parsed wrong: %+v", pods[0])
	}
	if pods[1].Ready {
		t.Errorf("booting pod should not be Ready: %+v", pods[1])
	}
	if !pods[2].Alloc {
		t.Errorf("taken pod should be Alloc: %+v", pods[2])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./controller/ -run TestParsePodList -v`
Expected: FAIL — `undefined: parsePodList` (if not yet added) or a parsing assertion failure.

- [ ] **Step 3: Write minimal implementation** (add to `controller/k8s.go`)

```go
import "encoding/json"

// parsePodList maps a k8s PodList into the controller's Pod view.
func parsePodList(body []byte) ([]Pod, error) {
	var pl struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				PodIP      string `json:"podIP"`
				Conditions []struct {
					Type, Status string
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &pl); err != nil {
		return nil, err
	}
	out := make([]Pod, 0, len(pl.Items))
	for _, it := range pl.Items {
		ready := false
		for _, c := range it.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				ready = true
			}
		}
		out = append(out, Pod{
			Name:  it.Metadata.Name,
			Game:  it.Metadata.Labels["game"],
			IP:    it.Status.PodIP,
			Ready: ready,
			Alloc: it.Metadata.Labels["alloc"] == "true",
		})
	}
	return out, nil
}
```

Also add `listPods`, which uses `do` (Task 2) + `parsePodList`:

```go
// listPods returns minigame pods of a game, parsed into the controller's Pod view.
func (k *kube) listPods(game string) ([]Pod, error) {
	b, code, err := k.do("GET", "/api/v1/namespaces/"+namespace+"/pods?labelSelector=app%3Dminigame,game%3D"+game, "", "")
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("listPods: %d %s", code, b)
	}
	return parsePodList(b)
}
```

> Add the `encoding/json` import to the existing import block in `k8s.go` (merge, don't duplicate).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./controller/ -run TestParsePodList -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add controller/k8s.go controller/parse_test.go
git commit -m "feat(controller): parse k8s PodList into pool view"
```

---

### Task 4: Velocity-register client

**Files:**
- Create: `controller/velocity.go`
- Test: `controller/velocity_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `type vel struct{ base, token string; hc *http.Client }`; `func newVel(base, token string) *vel`; methods `(*vel) register(name, addr string) error`, `unregister(name string) error`.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVelRegister(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	v := newVel(srv.URL, "sekret")
	if err := v.register("mg-stub-abc", "10.0.0.5:25565"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "POST" || gotPath != "/servers" {
		t.Errorf("register hit %s %s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer sekret" {
		t.Errorf("auth = %q", gotAuth)
	}
	if !strings.Contains(gotBody, "mg-stub-abc") || !strings.Contains(gotBody, "10.0.0.5:25565") {
		t.Errorf("body = %q", gotBody)
	}
}

func TestVelUnregister(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(200)
	}))
	defer srv.Close()

	if err := newVel(srv.URL, "x").unregister("mg-stub-abc"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "DELETE" || gotPath != "/servers/mg-stub-abc" {
		t.Errorf("unregister hit %s %s", gotMethod, gotPath)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./controller/ -run 'TestVel' -v`
Expected: FAIL — `undefined: newVel`.

- [ ] **Step 3: Write minimal implementation**

```go
package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// vel calls the velocity-register plugin to add/remove backends live.
type vel struct {
	base, token string
	hc          *http.Client
}

func newVel(base, token string) *vel {
	return &vel{base: base, token: token, hc: &http.Client{Timeout: 5 * time.Second}}
}

func (v *vel) req(method, path, body string) error {
	req, err := http.NewRequest(method, v.base+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+v.token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := v.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("velocity-register %s %s: %d", method, path, resp.StatusCode)
	}
	return nil
}

// register is an idempotent upsert; re-registering the same name updates it.
func (v *vel) register(name, addr string) error {
	return v.req("POST", "/servers", fmt.Sprintf(`{"name":%q,"address":%q}`, name, addr))
}

func (v *vel) unregister(name string) error {
	return v.req("DELETE", "/servers/"+name, "")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./controller/ -run 'TestVel' -v`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
git add controller/velocity.go controller/velocity_test.go
git commit -m "feat(controller): velocity-register HTTP client"
```

---

### Task 5: Controller server + reconcile loop (main)

**Files:**
- Create: `controller/main.go`
- Test: `controller/server_test.go`

**Interfaces:**
- Consumes: `Pod`, `needed`, `pickAllocatable` (Task 1); kube/vel real impls (Tasks 2–4).
- Produces: `type Controller struct{...}` with **func fields** (test seams, no interfaces): `listPods func(game string) ([]Pod, error)`, `createPod func(name string) error`, `deletePod func(name string) error`, `setAllocated func(name string) error`, `register func(name, addr string) error`, `unregister func(name string) error`. Handlers `handleAllocate`, `handleDone`; loop method `reconcile()`.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func newTestController(pods []Pod) (*Controller, *struct {
	sync.Mutex
	allocated, deleted, unregistered []string
}) {
	rec := &struct {
		sync.Mutex
		allocated, deleted, unregistered []string
	}{}
	c := &Controller{
		game: "stub", image: "mc/minigame-stub:dev", poolSize: 1,
		listPods: func(string) ([]Pod, error) { return pods, nil },
		createPod: func(string) error { return nil },
		setAllocated: func(n string) error {
			rec.Lock(); rec.allocated = append(rec.allocated, n); rec.Unlock(); return nil
		},
		deletePod: func(n string) error {
			rec.Lock(); rec.deleted = append(rec.deleted, n); rec.Unlock(); return nil
		},
		register:   func(string, string) error { return nil },
		unregister: func(n string) error {
			rec.Lock(); rec.unregistered = append(rec.unregistered, n); rec.Unlock(); return nil
		},
		registered: map[string]bool{},
	}
	return c, rec
}

func TestAllocateHappy(t *testing.T) {
	c, rec := newTestController([]Pod{{Name: "mg-stub-1", Game: "stub", Ready: true, IP: "10.0.0.5"}})
	r := httptest.NewRequest("POST", "/allocate", strings.NewReader(`{"game":"stub"}`))
	w := httptest.NewRecorder()
	c.handleAllocate(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d, body %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "mg-stub-1") || !strings.Contains(w.Body.String(), "10.0.0.5:25565") {
		t.Errorf("body = %s", w.Body)
	}
	if len(rec.allocated) != 1 || rec.allocated[0] != "mg-stub-1" {
		t.Errorf("did not mark allocated: %v", rec.allocated)
	}
}

func TestAllocateNoneReady(t *testing.T) {
	c, _ := newTestController([]Pod{{Name: "mg-stub-1", Game: "stub", Ready: false}})
	r := httptest.NewRequest("POST", "/allocate", strings.NewReader(`{"game":"stub"}`))
	w := httptest.NewRecorder()
	c.handleAllocate(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("code = %d, want 503", w.Code)
	}
}

func TestDoneDeletesAndUnregisters(t *testing.T) {
	c, rec := newTestController([]Pod{{Name: "mg-stub-1", Game: "stub", Ready: true, Alloc: true}})
	r := httptest.NewRequest("POST", "/instances/mg-stub-1/done", nil)
	w := httptest.NewRecorder()
	c.handleDone(w, r)
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	if len(rec.deleted) != 1 || rec.deleted[0] != "mg-stub-1" {
		t.Errorf("deleted = %v", rec.deleted)
	}
	if len(rec.unregistered) != 1 || rec.unregistered[0] != "mg-stub-1" {
		t.Errorf("unregistered = %v", rec.unregistered)
	}
}

func TestDoneUnknownPod404(t *testing.T) {
	c, _ := newTestController([]Pod{{Name: "mg-stub-1", Game: "stub"}})
	r := httptest.NewRequest("POST", "/instances/ghost/done", nil)
	w := httptest.NewRecorder()
	c.handleDone(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./controller/ -run 'TestAllocate|TestDone' -v`
Expected: FAIL — `undefined: Controller`.

- [ ] **Step 3: Write minimal implementation**

```go
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Controller owns the warm pool. Dependencies are func fields so tests inject fakes
// without interfaces or a real cluster.
type Controller struct {
	game, image string
	poolSize    int

	listPods     func(game string) ([]Pod, error)
	createPod    func(name string) error
	deletePod    func(name string) error
	setAllocated func(name string) error
	register     func(name, addr string) error
	unregister   func(name string) error

	mu         sync.Mutex     // ponytail: one global lock; split per-game if many games churn concurrently
	registered map[string]bool
}

func (c *Controller) handleAllocate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Game string `json:"game"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Game == "" {
		body.Game = c.game
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	pods, err := c.listPods(body.Game)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	p := pickAllocatable(pods, body.Game)
	if p == nil {
		http.Error(w, "no ready instance", http.StatusServiceUnavailable)
		return
	}
	if err := c.setAllocated(p.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{
		"server":  p.Name,
		"address": p.IP + ":25565",
	})
}

func (c *Controller) handleDone(w http.ResponseWriter, r *http.Request) {
	// path: /instances/{id}/done
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/instances/"), "/done")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "bad instance id", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	pods, err := c.listPods(c.game)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	known := false
	for _, p := range pods {
		if p.Name == id {
			known = true
		}
	}
	if !known {
		http.Error(w, "unknown instance", http.StatusNotFound)
		return
	}
	if err := c.unregister(id); err != nil {
		log.Printf("done: unregister %s: %v", id, err)
	}
	delete(c.registered, id)
	if err := c.deletePod(id); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// reconcile refills the pool and syncs Velocity registration. Called on a ticker.
func (c *Controller) reconcile() {
	c.mu.Lock()
	defer c.mu.Unlock()
	pods, err := c.listPods(c.game)
	if err != nil {
		log.Printf("reconcile: list: %v", err)
		return
	}
	// refill
	for i := 0; i < needed(pods, c.poolSize); i++ {
		name := fmt.Sprintf("mg-%s-%s", c.game, randSuffix())
		if err := c.createPod(name); err != nil {
			log.Printf("reconcile: create %s: %v", name, err)
		}
	}
	// registration sync: register newly-Ready, unregister vanished
	seen := map[string]bool{}
	for _, p := range pods {
		seen[p.Name] = true
		if p.Ready && !c.registered[p.Name] {
			if err := c.register(p.Name, p.IP+":25565"); err != nil {
				log.Printf("reconcile: register %s: %v", p.Name, err)
				continue
			}
			c.registered[p.Name] = true
		}
	}
	for name := range c.registered {
		if !seen[name] {
			if err := c.unregister(name); err != nil {
				log.Printf("reconcile: unregister %s: %v", name, err)
			}
			delete(c.registered, name)
		}
	}
}

func randSuffix() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func main() {
	game := envOr("GAME", "stub")
	image := envOr("MINIGAME_IMAGE", "mc/minigame-stub:dev")
	poolSize, _ := strconv.Atoi(envOr("POOL_SIZE", "1"))
	velBase := envOr("VELOCITY_REGISTER_URL", "http://velocity.mc.svc.cluster.local:8080")
	controllerURL := envOr("CONTROLLER_URL", "http://controller.mc.svc.cluster.local:8080")

	k, err := newKube()
	if err != nil {
		log.Fatalf("k8s: %v", err)
	}
	token, err := os.ReadFile("/secret/controller.token")
	if err != nil {
		log.Fatalf("read controller token: %v", err)
	}
	v := newVel(velBase, strings.TrimSpace(string(token)))

	c := &Controller{
		game: game, image: image, poolSize: poolSize,
		listPods:     k.listPods,
		createPod:    func(name string) error { return k.createPod(name, game, image, controllerURL) },
		deletePod:    k.deletePod,
		setAllocated: k.setAllocated,
		register:     v.register,
		unregister:   v.unregister,
		registered:   map[string]bool{},
	}

	go func() {
		for range time.Tick(2 * time.Second) {
			c.reconcile()
		}
	}()

	http.HandleFunc("/allocate", c.handleAllocate)
	http.HandleFunc("/instances/", c.handleDone)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	log.Printf("controller up: game=%s pool=%d", game, poolSize)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 4: Run the full controller test suite**

Run: `go test ./controller/ -v`
Expected: PASS — all tests from Tasks 1–5.

- [ ] **Step 5: Commit**

```bash
git add controller/main.go controller/server_test.go
git commit -m "feat(controller): REST server + reconcile loop (allocate/done/refill/register-sync)"
```

---

### Task 6: Controller image + Makefile build

**Files:**
- Create: `images/controller/Dockerfile`
- Modify: `Makefile`

**Interfaces:**
- Consumes: the `controller/` Go package (Tasks 1–5).
- Produces: image `mc/controller:dev`; make targets `build-controller`, and `load`/`up` wiring (full wiring of `apply` is Task 11).

- [ ] **Step 1: Write the Dockerfile**

```dockerfile
# Build context = repo root (needs go.mod + controller/). Build with:
#   docker build -f images/controller/Dockerfile -t mc/controller:dev .
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod ./
COPY controller/ ./controller/
RUN CGO_ENABLED=0 go build -o /controller ./controller

FROM gcr.io/distroless/static-debian12
COPY --from=build /controller /controller
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/controller"]
```

- [ ] **Step 2: Add the Makefile target**

Add to the `.PHONY` line: `build-controller`. Add this target near the other image builds:

```makefile
build-controller:
	docker build -f images/controller/Dockerfile -t mc/controller:dev .
```

And extend `load` so the controller image is built and loaded (append to the existing `load` recipe):

```makefile
load: build-velocity build-lobby build-controller build-minigame-stub
	kind load docker-image mc/velocity:dev --name $(CLUSTER)
	kind load docker-image mc/lobby:dev --name $(CLUSTER)
	kind load docker-image mc/controller:dev --name $(CLUSTER)
	kind load docker-image mc/minigame-stub:dev --name $(CLUSTER)
```

> `build-minigame-stub` is created in Task 9. If executing strictly in order, temporarily add only `build-controller` + its two new `kind load` lines now, and add the `minigame-stub` lines in Task 9. Do not reference a target that doesn't exist yet.

- [ ] **Step 3: Build the image to verify it compiles in-container**

Run: `make build-controller`
Expected: a successful multi-stage build ending with `naming to docker.io/mc/controller:dev`.

- [ ] **Step 4: Commit**

```bash
git add images/controller/Dockerfile Makefile
git commit -m "build(controller): image + make build-controller"
```

---

### Task 7: velocity-register plugin (Velocity API)

**Files:**
- Create: `plugins/velocity-register/build.gradle`
- Create: `plugins/velocity-register/src/main/java/mc/velreg/VelRegPlugin.java`
- Create: `plugins/velocity-register/src/main/java/mc/velreg/ServersHandler.java`
- Create: `plugins/velocity-register/src/main/java/mc/velreg/Registry.java`
- Test: `plugins/velocity-register/src/test/java/mc/velreg/ServersHandlerTest.java`

**Interfaces:**
- Consumes: nothing.
- Produces: jar `velocity-register.jar` exposing `POST /servers {name,address}` and `DELETE /servers/{name}` on `:8080`, bearer-token gated. Decision logic in pure `ServersHandler.handle(method, path, auth, body, token, Registry) -> int`.

- [ ] **Step 1: Write the failing test**

```java
package mc.velreg;

import org.junit.jupiter.api.Test;
import java.util.*;
import static org.junit.jupiter.api.Assertions.*;

class ServersHandlerTest {
  static final class FakeRegistry implements Registry {
    final Map<String,String> servers = new HashMap<>();
    public void register(String name, String address) { servers.put(name, address); }
    public boolean unregister(String name) { return servers.remove(name) != null; }
  }

  @Test void registersOnPost() {
    var reg = new FakeRegistry();
    int code = ServersHandler.handle("POST", "/servers", "Bearer t",
        "{\"name\":\"mg-stub-1\",\"address\":\"10.0.0.5:25565\"}", "t", reg);
    assertEquals(200, code);
    assertEquals("10.0.0.5:25565", reg.servers.get("mg-stub-1"));
  }

  @Test void unregistersOnDelete() {
    var reg = new FakeRegistry();
    reg.register("mg-stub-1", "10.0.0.5:25565");
    int code = ServersHandler.handle("DELETE", "/servers/mg-stub-1", "Bearer t", "", "t", reg);
    assertEquals(200, code);
    assertTrue(reg.servers.isEmpty());
  }

  @Test void rejectsBadToken() {
    var reg = new FakeRegistry();
    int code = ServersHandler.handle("POST", "/servers", "Bearer wrong",
        "{\"name\":\"x\",\"address\":\"y\"}", "t", reg);
    assertEquals(401, code);
    assertTrue(reg.servers.isEmpty());
  }

  @Test void unknownDeleteIs404() {
    int code = ServersHandler.handle("DELETE", "/servers/ghost", "Bearer t", "", "t", new FakeRegistry());
    assertEquals(404, code);
  }
}
```

- [ ] **Step 2: Write build.gradle**

```groovy
plugins { id 'java' }
group = 'mc'
version = '0.1.0'
java { toolchain { languageVersion = JavaLanguageVersion.of(21) } }
repositories {
  mavenCentral()
  maven { url = 'https://repo.papermc.io/repository/maven-public/' }
}
dependencies {
  compileOnly 'com.velocitypowered:velocity-api:3.4.0-SNAPSHOT'
  annotationProcessor 'com.velocitypowered:velocity-api:3.4.0-SNAPSHOT'
  testImplementation 'org.junit.jupiter:junit-jupiter:5.10.2'
  testRuntimeOnly 'org.junit.platform:junit-platform-launcher'  // required since Gradle 9
}
test { useJUnitPlatform() }
// ponytail: plain jar; the handler uses only the JDK (com.sun.net.httpserver) + velocity-api.
jar { archiveFileName = 'velocity-register.jar' }
```

- [ ] **Step 3: Run test to verify it fails**

Run: `docker run --rm -u $(id -u):$(id -g) -e GRADLE_USER_HOME=/work/.gradle -v $PWD/plugins/velocity-register:/work -w /work gradle:jdk21 gradle test`
Expected: FAIL — `ServersHandler`, `Registry` not found (compilation error).

- [ ] **Step 4: Write Registry.java**

```java
package mc.velreg;

/** Minimal backend registry, so the handler is testable without a live proxy. */
public interface Registry {
  void register(String name, String address);   // idempotent upsert
  boolean unregister(String name);               // false if name was unknown
}
```

- [ ] **Step 5: Write ServersHandler.java** (pure decision logic + a tiny field extractor)

```java
package mc.velreg;

import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Pure request logic: no HttpExchange, no proxy — testable with a fake Registry. */
public final class ServersHandler {
  private ServersHandler() {}

  private static final Pattern NAME = Pattern.compile("\"name\"\\s*:\\s*\"([^\"]+)\"");
  private static final Pattern ADDR = Pattern.compile("\"address\"\\s*:\\s*\"([^\"]+)\"");

  /** Returns the HTTP status code; mutates the registry on success. */
  public static int handle(String method, String path, String auth, String body, String token, Registry reg) {
    if (token != null && !token.isEmpty() && !("Bearer " + token).equals(auth)) return 401;
    switch (method) {
      case "POST": {
        if (!path.equals("/servers")) return 404;
        String name = group(NAME, body), addr = group(ADDR, body);
        if (name == null || addr == null) return 400;
        reg.register(name, addr);
        return 200;
      }
      case "DELETE": {
        String prefix = "/servers/";
        if (!path.startsWith(prefix) || path.length() <= prefix.length()) return 400;
        String name = path.substring(prefix.length());
        return reg.unregister(name) ? 200 : 404;
      }
      default:
        return 405;
    }
  }

  // ponytail: regex field extract for two known keys; swap for Gson if the payload grows.
  private static String group(Pattern p, String s) {
    if (s == null) return null;
    Matcher m = p.matcher(s);
    return m.find() ? m.group(1) : null;
  }
}
```

- [ ] **Step 6: Write VelRegPlugin.java** (the Velocity glue + HTTP adapter)

```java
package mc.velreg;

import com.google.inject.Inject;
import com.velocitypowered.api.event.Subscribe;
import com.velocitypowered.api.event.proxy.ProxyInitializeEvent;
import com.velocitypowered.api.plugin.Plugin;
import com.velocitypowered.api.proxy.ProxyServer;
import com.velocitypowered.api.proxy.server.ServerInfo;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.io.InputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;

@Plugin(id = "velocity-register", name = "VelocityRegister", version = "0.1.0")
public final class VelRegPlugin {
  private final ProxyServer proxy;

  @Inject public VelRegPlugin(ProxyServer proxy) { this.proxy = proxy; }

  @Subscribe public void onInit(ProxyInitializeEvent e) throws IOException {
    String token = System.getenv("CONTROLLER_TOKEN");

    // Adapt the live proxy to the Registry interface.
    Registry reg = new Registry() {
      public void register(String name, String address) {
        proxy.unregisterServer(new ServerInfo(name, parse(address))); // drop stale, then add (upsert)
        proxy.registerServer(new ServerInfo(name, parse(address)));
      }
      public boolean unregister(String name) {
        return proxy.getServer(name).map(s -> { proxy.unregisterServer(s.getServerInfo()); return true; }).orElse(false);
      }
    };

    HttpServer http = HttpServer.create(new InetSocketAddress("0.0.0.0", 8080), 0);
    http.createContext("/servers", ex -> {
      String body;
      try (InputStream in = ex.getRequestBody()) { body = new String(in.readAllBytes(), StandardCharsets.UTF_8); }
      int code = ServersHandler.handle(ex.getRequestMethod(), ex.getRequestURI().getPath(),
          ex.getRequestHeaders().getFirst("Authorization"), body, token, reg);
      ex.sendResponseHeaders(code, -1);
      ex.close();
    });
    http.start();
  }

  private static InetSocketAddress parse(String hostPort) {
    int i = hostPort.lastIndexOf(':');
    return new InetSocketAddress(hostPort.substring(0, i), Integer.parseInt(hostPort.substring(i + 1)));
  }
}
```

- [ ] **Step 7: Run test to verify it passes**

Run: `docker run --rm -u $(id -u):$(id -g) -e GRADLE_USER_HOME=/work/.gradle -v $PWD/plugins/velocity-register:/work -w /work gradle:jdk21 gradle test`
Expected: PASS (4 tests).

- [ ] **Step 8: Commit**

```bash
git add plugins/velocity-register
git commit -m "feat(velocity-register): live server register/unregister plugin + handler tests"
```

---

### Task 8: Bake velocity-register into the Velocity image

**Files:**
- Modify: `images/velocity/Dockerfile`
- Modify: `Makefile`

**Interfaces:**
- Consumes: `velocity-register.jar` (Task 7).
- Produces: Velocity image with the plugin in `/app/plugins/`, listening on `:8080`, reading `CONTROLLER_TOKEN` from env.

- [ ] **Step 1: Add the plugin build to the Makefile**

Add a target and make `build-velocity` depend on building the jar. Add `build-velreg` to `.PHONY`. Insert:

```makefile
VELREG_GRADLE := docker run --rm -u $(shell id -u):$(shell id -g) \
  -e GRADLE_USER_HOME=/work/.gradle \
  -v $(PWD)/plugins/velocity-register:/work -w /work gradle:jdk21 gradle

build-velreg:
	$(VELREG_GRADLE) build
	cp plugins/velocity-register/build/libs/velocity-register.jar images/velocity/velocity-register.jar
```

Change the `build-velocity` target to build the jar first and clean up after:

```makefile
build-velocity: build-velreg
	docker build -t mc/velocity:dev \
	  --build-arg JRE_TAG=$(JRE_TAG) --build-arg VELOCITY_URL=$(VELOCITY_URL) \
	  images/velocity
	rm -f images/velocity/velocity-register.jar
```

- [ ] **Step 2: Update the Velocity Dockerfile**

```dockerfile
ARG JRE_TAG=21-jre
FROM eclipse-temurin:${JRE_TAG}
ARG VELOCITY_URL
WORKDIR /app
ADD ${VELOCITY_URL} /app/velocity.jar
COPY velocity.toml /app/velocity.toml
COPY velocity-register.jar /app/plugins/velocity-register.jar
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh && chmod 0644 /app/plugins/velocity-register.jar \
 && useradd -r mc && chown -R mc /app
USER mc
EXPOSE 25565 8080
ENTRYPOINT ["/entrypoint.sh"]
```

- [ ] **Step 3: Build to verify**

Run: `make build-velocity`
Expected: gradle builds `velocity-register.jar`, then `docker build` succeeds (`naming to ...mc/velocity:dev`).

- [ ] **Step 4: Commit**

```bash
git add images/velocity/Dockerfile Makefile
git commit -m "build(velocity): bake velocity-register plugin, expose :8080"
```

---

### Task 9: minigame-stub plugin + image

**Files:**
- Create: `plugins/stub-game/build.gradle`
- Create: `plugins/stub-game/src/main/java/mc/stub/StubPlugin.java`
- Create: `plugins/stub-game/src/main/resources/plugin.yml`
- Test: `plugins/stub-game/src/test/java/mc/stub/StubPluginTest.java`
- Create: `images/minigame-stub/Dockerfile`
- Create: `images/minigame-stub/worlds.yml`
- Modify: `Makefile`

**Interfaces:**
- Consumes: `mc/mc-base:dev`; the baked `worlds/lobby.slime` (reused as the stub world `game`).
- Produces: image `mc/minigame-stub:dev`; make target `build-minigame-stub`.

- [ ] **Step 1: Write the failing test** (the only pure bit — the `/done` URL builder)

```java
package mc.stub;

import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.*;

class StubPluginTest {
  @Test void buildsDoneUrl() {
    assertEquals("http://controller.mc.svc.cluster.local:8080/instances/mg-stub-abc/done",
        StubPlugin.doneUrl("http://controller.mc.svc.cluster.local:8080", "mg-stub-abc"));
  }
  @Test void trimsTrailingSlash() {
    assertEquals("http://c:8080/instances/x/done", StubPlugin.doneUrl("http://c:8080/", "x"));
  }
}
```

- [ ] **Step 2: Write build.gradle**

```groovy
plugins { id 'java' }
group = 'mc'
version = '0.1.0'
java { toolchain { languageVersion = JavaLanguageVersion.of(21) } }
repositories {
  mavenCentral()
  maven { url = 'https://repo.papermc.io/repository/maven-public/' }
}
dependencies {
  compileOnly 'io.papermc.paper:paper-api:1.21.8-R0.1-SNAPSHOT'
  testImplementation 'org.junit.jupiter:junit-jupiter:5.10.2'
  testRuntimeOnly 'org.junit.platform:junit-platform-launcher'  // required since Gradle 9
}
test { useJUnitPlatform() }
jar { archiveFileName = 'stub-plugin.jar' }
```

- [ ] **Step 3: Run test to verify it fails**

Run: `docker run --rm -u $(id -u):$(id -g) -e GRADLE_USER_HOME=/work/.gradle -v $PWD/plugins/stub-game:/work -w /work gradle:jdk21 gradle test`
Expected: FAIL — `StubPlugin` not found.

- [ ] **Step 4: Write plugin.yml**

```yaml
name: StubGame
version: 0.1.0
main: mc.stub.StubPlugin
api-version: '1.21'
commands:
  endgame:
    description: End this stub game and recycle the pod.
```

- [ ] **Step 5: Write StubPlugin.java**

```java
package mc.stub;

import org.bukkit.*;
import org.bukkit.command.*;
import org.bukkit.entity.Player;
import org.bukkit.event.*;
import org.bukkit.event.player.PlayerJoinEvent;
import org.bukkit.plugin.java.JavaPlugin;
import java.net.URI;
import java.net.http.*;

public final class StubPlugin extends JavaPlugin implements Listener {
  @Override public void onEnable() {
    getServer().getPluginManager().registerEvents(this, this);
  }

  @EventHandler public void onJoin(PlayerJoinEvent e) {
    Player p = e.getPlayer();
    // ponytail: reuse the lobby slime world named "game"; Slice 2 gives each game its own world.
    World w = Bukkit.getWorld("game");
    if (w != null) p.teleport(w.getSpawnLocation());
    p.setInvulnerable(true);
    p.sendMessage(ChatColor.GREEN + "Stub minigame. Op: /endgame to recycle this pod.");
  }

  @Override public boolean onCommand(CommandSender s, Command cmd, String label, String[] args) {
    if (!cmd.getName().equalsIgnoreCase("endgame")) return false;
    if (!s.isOp()) { s.sendMessage("op only"); return true; }
    String url = doneUrl(System.getenv("CONTROLLER_URL"), System.getenv("INSTANCE_ID"));
    s.sendMessage("ending game -> " + url);
    // async: don't block the main thread on a network call.
    getServer().getScheduler().runTaskAsynchronously(this, () -> postDone(url));
    return true;
  }

  /** POST controller /done; the controller then unregisters + deletes this pod. */
  static String doneUrl(String base, String instanceId) {
    if (base.endsWith("/")) base = base.substring(0, base.length() - 1);
    return base + "/instances/" + instanceId + "/done";
  }

  private void postDone(String url) {
    try {
      HttpClient.newHttpClient().send(
          HttpRequest.newBuilder(URI.create(url)).POST(HttpRequest.BodyPublishers.noBody()).build(),
          HttpResponse.BodyHandlers.discarding());
    } catch (Exception ex) {
      getLogger().warning("POST done failed: " + ex.getMessage());
    }
  }
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `docker run --rm -u $(id -u):$(id -g) -e GRADLE_USER_HOME=/work/.gradle -v $PWD/plugins/stub-game:/work -w /work gradle:jdk21 gradle test`
Expected: PASS (2 tests).

- [ ] **Step 7: Write images/minigame-stub/worlds.yml** (SWM world `game`, same shape as the lobby's)

```yaml
# Loaded by SWM at startup; readOnly so the minigame stays disposable.
worlds:
  game:
    source: file
    difficulty: peaceful
    spawn: 0, -60, 0
    allowMonsters: false
    allowAnimals: false
    loadOnStartup: true
    readOnly: true
    saveBlockTicks: false
    saveFluidTicks: false
    savePoi: false
```

- [ ] **Step 8: Write images/minigame-stub/Dockerfile**

```dockerfile
FROM mc/mc-base:dev
# Stub plugin + a baked slime world named "game" (reuses the lobby world) + SWM world entry.
COPY stub-plugin.jar /server/plugins/stub-plugin.jar
COPY game.slime /worlds/game.slime
COPY worlds.yml /opt/config/plugins/SlimeWorldManager/worlds.yml
```

- [ ] **Step 9: Add the Makefile target**

Add `build-minigame-stub` to `.PHONY`. Add:

```makefile
STUB_GRADLE := docker run --rm -u $(shell id -u):$(shell id -g) \
  -e GRADLE_USER_HOME=/work/.gradle \
  -v $(PWD)/plugins/stub-game:/work -w /work gradle:jdk21 gradle

build-minigame-stub: build-base
	$(STUB_GRADLE) build
	cp plugins/stub-game/build/libs/stub-plugin.jar images/minigame-stub/stub-plugin.jar
	cp worlds/lobby.slime images/minigame-stub/game.slime
	docker build -t mc/minigame-stub:dev images/minigame-stub
	rm -f images/minigame-stub/stub-plugin.jar images/minigame-stub/game.slime
```

- [ ] **Step 10: Build to verify**

Run: `make build-minigame-stub`
Expected: gradle test+build pass, image `mc/minigame-stub:dev` built. (Requires `worlds/lobby.slime` — produced by `make lobby-world`; run it first if absent.)

- [ ] **Step 11: Commit**

```bash
git add plugins/stub-game images/minigame-stub Makefile
git commit -m "feat(minigame-stub): placeholder game image + self-recycle via /endgame"
```

---

### Task 10: Wire the lobby compass to allocate + Connect

**Files:**
- Modify: `plugins/lobby-plugin/src/main/java/mc/lobby/LobbyPlugin.java`
- Modify: `plugins/lobby-plugin/src/main/resources/config.yml`
- Create: `plugins/lobby-plugin/src/main/java/mc/lobby/Allocate.java`
- Test: `plugins/lobby-plugin/src/test/java/mc/lobby/AllocateTest.java`

**Interfaces:**
- Consumes: controller `POST /allocate` (Task 5).
- Produces: clicking a menu entry sends the player to a freshly-allocated instance. Pure helper `Allocate.parseServer(String json) -> String`.

- [ ] **Step 1: Write the failing test**

```java
package mc.lobby;

import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.*;

class AllocateTest {
  @Test void extractsServerName() {
    assertEquals("mg-stub-abc",
        Allocate.parseServer("{\"server\":\"mg-stub-abc\",\"address\":\"10.0.0.5:25565\"}"));
  }
  @Test void returnsNullWhenAbsent() {
    assertNull(Allocate.parseServer("{\"error\":\"no ready instance\"}"));
  }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker run --rm -u $(id -u):$(id -g) -e GRADLE_USER_HOME=/work/.gradle -v $PWD/plugins/lobby-plugin:/work -w /work gradle:jdk21 gradle test`
Expected: FAIL — `Allocate` not found.

- [ ] **Step 3: Write Allocate.java**

```java
package mc.lobby;

import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Pure helper for parsing the controller's /allocate response. */
public final class Allocate {
  private Allocate() {}
  private static final Pattern SERVER = Pattern.compile("\"server\"\\s*:\\s*\"([^\"]+)\"");

  // ponytail: regex extract of one field; swap for a JSON lib if the response grows.
  public static String parseServer(String json) {
    if (json == null) return null;
    Matcher m = SERVER.matcher(json);
    return m.find() ? m.group(1) : null;
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `docker run --rm -u $(id -u):$(id -g) -e GRADLE_USER_HOME=/work/.gradle -v $PWD/plugins/lobby-plugin:/work -w /work gradle:jdk21 gradle test`
Expected: PASS (Menu + Allocate tests).

- [ ] **Step 5: Update config.yml** (one stub entry + controller URL)

```yaml
# Slice 1: the only real game is the stub placeholder; both menu slots route to it.
controller: http://controller.mc.svc.cluster.local:8080
minigames:
  - { name: Stub Game, material: GRASS_BLOCK, target: stub }
```

- [ ] **Step 6: Edit LobbyPlugin.java** — register the outgoing channel and replace the `onClick` stub.

In `onEnable`, after `getServer().getPluginManager().registerEvents(this, this);` add:

```java
    getServer().getMessenger().registerOutgoingPluginChannel(this, "BungeeCord");
```

Replace the entire `onClick` method with:

```java
  @EventHandler public void onClick(InventoryClickEvent e) {
    if (!TITLE.equals(e.getView().getTitle())) return;
    e.setCancelled(true);
    if (e.getCurrentItem() == null) return;
    int slot = e.getRawSlot();
    if (slot < 0 || slot >= entries.size()) return;
    String game = entries.get(slot).target();
    Player p = (Player) e.getWhoClicked();
    p.closeInventory();
    String base = getConfig().getString("controller", "http://controller.mc.svc.cluster.local:8080");
    // async: never block the main thread on the allocate HTTP call.
    getServer().getScheduler().runTaskAsynchronously(this, () -> {
      String server = allocate(base, game);
      if (server == null) {
        p.sendMessage(ChatColor.RED + "No " + game + " server available, try again.");
        return;
      }
      getServer().getScheduler().runTask(this, () -> connect(p, server));
    });
  }

  private String allocate(String base, String game) {
    try {
      var resp = java.net.http.HttpClient.newHttpClient().send(
          java.net.http.HttpRequest.newBuilder(java.net.URI.create(base + "/allocate"))
              .header("Content-Type", "application/json")
              .POST(java.net.http.HttpRequest.BodyPublishers.ofString("{\"game\":\"" + game + "\"}"))
              .build(),
          java.net.http.HttpResponse.BodyHandlers.ofString());
      return resp.statusCode() == 200 ? Allocate.parseServer(resp.body()) : null;
    } catch (Exception ex) {
      getLogger().warning("allocate failed: " + ex.getMessage());
      return null;
    }
  }

  private void connect(Player p, String server) {
    com.google.common.io.ByteArrayDataOutput out = com.google.common.io.ByteStreams.newDataOutput();
    out.writeUTF("Connect");
    out.writeUTF(server);
    p.sendPluginMessage(this, "BungeeCord", out.toByteArray());
  }
```

- [ ] **Step 7: Run the lobby test suite (full build)**

Run: `docker run --rm -u $(id -u):$(id -g) -e GRADLE_USER_HOME=/work/.gradle -v $PWD/plugins/lobby-plugin:/work -w /work gradle:jdk21 gradle build`
Expected: PASS — compiles and all tests green.

- [ ] **Step 8: Commit**

```bash
git add plugins/lobby-plugin
git commit -m "feat(lobby): compass click allocates an instance and Connects the player"
```

---

### Task 11: k8s manifests — controller RBAC/Deployment/Service + velocity token/port

**Files:**
- Create: `deploy/k8s/controller.yaml`
- Modify: `deploy/k8s/velocity.yaml`
- Modify: `Makefile` (the `apply` target)

**Interfaces:**
- Consumes: images `mc/controller:dev`, `mc/velocity:dev` with the plugin (Tasks 6, 8); secret `velocity-forwarding`; new secret `controller-token`.
- Produces: a running controller with pod-management RBAC, reachable at `controller.mc.svc.cluster.local:8080`; Velocity reachable for registration at `velocity.mc.svc.cluster.local:8080`.

- [ ] **Step 1: Write deploy/k8s/controller.yaml**

```yaml
apiVersion: v1
kind: ServiceAccount
metadata: { name: controller, namespace: mc }
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: { name: controller, namespace: mc }
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch", "create", "delete", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: { name: controller, namespace: mc }
roleRef: { apiGroup: rbac.authorization.k8s.io, kind: Role, name: controller }
subjects:
  - { kind: ServiceAccount, name: controller, namespace: mc }
---
apiVersion: apps/v1
kind: Deployment
metadata: { name: controller, namespace: mc }
spec:
  replicas: 1
  selector: { matchLabels: { app: controller } }
  template:
    metadata: { labels: { app: controller } }
    spec:
      serviceAccountName: controller
      containers:
        - name: controller
          image: mc/controller:dev
          imagePullPolicy: IfNotPresent
          ports: [ { containerPort: 8080 } ]
          env:
            - { name: GAME, value: stub }
            - { name: POOL_SIZE, value: "1" }
            - { name: MINIGAME_IMAGE, value: mc/minigame-stub:dev }
            - { name: VELOCITY_REGISTER_URL, value: http://velocity.mc.svc.cluster.local:8080 }
            - { name: CONTROLLER_URL, value: http://controller.mc.svc.cluster.local:8080 }
          volumeMounts:
            - { name: token, mountPath: /secret, readOnly: true }
          readinessProbe:
            httpGet: { path: /healthz, port: 8080 }
            initialDelaySeconds: 3
            periodSeconds: 5
      volumes:
        - name: token
          secret:
            secretName: controller-token
            items: [ { key: controller.token, path: controller.token } ]
---
apiVersion: v1
kind: Service
metadata: { name: controller, namespace: mc }
spec:
  selector: { app: controller }
  ports: [ { port: 8080, targetPort: 8080 } ]
```

- [ ] **Step 2: Modify deploy/k8s/velocity.yaml** — add the `:8080` container/Service port and inject `CONTROLLER_TOKEN`.

The current file (read it first) has `ports: [ { containerPort: 25565 } ]` and no `env:` on the velocity container, and a `LoadBalancer` Service with `ports: [ { port: 25565, targetPort: 25565, protocol: TCP } ]`.

Replace the container's `ports:` line and add an `env:` block immediately after it:

```yaml
          ports: [ { containerPort: 25565 }, { containerPort: 8080 } ]
          env:
            - name: CONTROLLER_TOKEN
              valueFrom:
                secretKeyRef: { name: controller-token, key: controller.token }
```

Replace the Service's `ports:` line with both named ports:

```yaml
  ports:
    - { name: mc, port: 25565, targetPort: 25565, protocol: TCP }
    - { name: register, port: 8080, targetPort: 8080, protocol: TCP }
```

> This Service is `type: LoadBalancer`, so 8080 also lands on the external LB IP — acceptable for local `kind`; the bearer token gates every request. `ponytail: same Service for both ports; split a ClusterIP for 8080 if external exposure ever matters.`

- [ ] **Step 3: Update the Makefile `apply` target** — create `controller-token` and apply `controller.yaml`.

```makefile
apply:
	kubectl -n mc create secret generic velocity-forwarding \
	  --from-literal=forwarding.secret=$$(openssl rand -hex 24) \
	  --dry-run=client -o yaml | kubectl apply -f -
	kubectl -n mc create secret generic controller-token \
	  --from-literal=controller.token=$$(openssl rand -hex 24) \
	  --dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -f deploy/k8s/velocity.yaml -f deploy/k8s/lobby.yaml -f deploy/k8s/controller.yaml
```

- [ ] **Step 4: Validate manifests parse** (no cluster mutation)

Run: `kubectl apply --dry-run=client -f deploy/k8s/controller.yaml && kubectl apply --dry-run=client -f deploy/k8s/velocity.yaml`
Expected: each resource prints `... (dry run)` with no errors.

- [ ] **Step 5: Commit**

```bash
git add deploy/k8s/controller.yaml deploy/k8s/velocity.yaml Makefile
git commit -m "deploy(controller): RBAC + Deployment + Service; velocity token + :8080"
```

---

### Task 12: End-to-end bring-up, docs, and verification

**Files:**
- Modify: `AGENTS.md`
- Modify: `docs/superpowers/SLICE-1-HANDOFF.md` (mark Slice 1 done; add Slice 2 carryover) — or create `docs/superpowers/SLICE-2-HANDOFF.md`
- Modify: `Makefile` (optional `make game-smoke` convenience target)

**Interfaces:**
- Consumes: everything from Tasks 1–11.
- Produces: a verified end-to-end loop and updated handoff docs.

- [ ] **Step 1: Full Go + plugin unit suites green**

Run: `go test ./...`
Expected: PASS (controller package).
Run each plugin: `gradle test` (via the dockerized commands above) for `velocity-register`, `stub-game`, `lobby-plugin`.
Expected: PASS.

- [ ] **Step 2: Bring the cluster up**

Run: `make down; make up` (then Ctrl-C once the LoadBalancer IP is assigned).
Expected: namespace, secrets, velocity, lobby, controller applied. `kubectl -n mc get pods` shows `controller`, `velocity`, `lobby`, and after ~30s one `mg-stub-<hex>` pod (the warm pool, POOL_SIZE=1) reaching `Running`/Ready.

- [ ] **Step 3: Verify registration**

Run: `kubectl -n mc logs deploy/controller`
Expected: a line showing the stub pod registered (no repeated `register ... error`).
Run: `kubectl -n mc get pods -l app=minigame -o wide`
Expected: one Ready `mg-stub-*` pod with an IP.

- [ ] **Step 4: Manual end-to-end (the real acceptance test)**

1. Get the LB IP: `kubectl -n mc get svc velocity -o jsonpath='{.status.loadBalancer.ingress[0].ip}'`.
2. Join that IP:25565 with a 1.21.8 client → land in the lobby.
3. Right-click the compass → menu opens → click **Stub Game**.
4. Expected: you are sent to the stub server ("Stub minigame…" message); a second `mg-stub-*` pod appears (pool refilled) and the one you joined now has label `alloc=true` (`kubectl -n mc get pods -l app=minigame -L alloc`).
5. As op, run `/endgame` on the stub server.
6. Expected: controller logs show unregister + delete; your `mg-stub-*` pod terminates; the pool returns to one Ready unallocated pod. (Your client is dropped from that backend; `/server lobby` or rejoin returns to lobby.)

- [ ] **Step 5: Update AGENTS.md** — mark Slice 1 done, point at Slice 2.

In `## Current state`, change the Slice 1 line to:

```markdown
- **Slice 1 (Controller + dynamic registration) — DONE, merged to `main`.** Go controller
  owns a warm pool of bare minigame Pods (k8s REST, no client-go), self-recycled via
  `POST /done`; velocity-register plugin registers backends live; compass click allocates
  + Connects. Placeholder game: `mc/minigame-stub:dev`.
- **Slice 2 (Minigame image convention) — NEXT, not started.**
```

- [ ] **Step 6: Write the Slice 2 handoff** (`docs/superpowers/SLICE-2-HANDOFF.md`) capturing carryover:

```markdown
# Slice 2 handoff (read this first in a fresh session)

Slice 1 merged. The controller + dynamic registration loop works end-to-end with one
placeholder game (`mc/minigame-stub:dev`). Slice 2 = the **minigame image convention**.

## Carryover facts (don't re-derive)
- Controller env knobs: `GAME`, `POOL_SIZE`, `MINIGAME_IMAGE`, `VELOCITY_REGISTER_URL`,
  `CONTROLLER_URL`. Today it manages ONE game type; Slice 2 likely needs multiple
  (per-game image + pool size). The pool/registration logic already keys on the `game` label.
- A minigame pod is a bare Pod (labels `app=minigame, game=<g>, alloc=<bool>`), mounts the
  forwarding secret, env `INSTANCE_ID`+`CONTROLLER_URL`, TCP readiness on 25565.
- Game-over contract: the pod POSTs `{CONTROLLER_URL}/instances/{INSTANCE_ID}/done`; the
  stub triggers it from an op `/endgame` command. Real games call it when their match ends.
- velocity-register: `POST /servers {name,address}` / `DELETE /servers/{name}` on :8080,
  bearer `CONTROLLER_TOKEN`. Idempotent upsert.
- Do NOT build a scaffolding/templating generator until 2–3 games hurt by hand (roadmap).

## Open Slice 2 question to settle first
- Multi-game config: per-game image + pool size. Where does it live — controller env list,
  a ConfigMap, or a CRD? Recommended start: a small ConfigMap of `{game: {image, poolSize}}`.
```

- [ ] **Step 7: Commit**

```bash
git add AGENTS.md docs/superpowers/SLICE-2-HANDOFF.md Makefile
git commit -m "docs(slice-1): mark done, add Slice 2 handoff"
```

- [ ] **Step 8: Finish the branch**

Use the `superpowers:finishing-a-development-branch` skill to merge `slice-1-controller` into `main` (`--no-ff`) after the manual acceptance test passes.

---

## Notes for the implementer

- **Package compiles after every task.** Tasks 2/3 are split for review clarity, but `parsePodList` must exist as soon as `listPods` references it — implement it with Task 2's file, add only its test in Task 3.
- **Velocity API version**: if `3.4.0-SNAPSHOT` isn't resolvable, check `images/versions.env` (`VELOCITY_URL` build 559) and use the matching `velocity-api` snapshot from `https://repo.papermc.io`.
- **`registerServer` upsert**: Velocity throws if a server name already exists; `VelRegPlugin` unregisters-then-registers to make it a true upsert. Keep that order.
- **No new Go deps**: if you reach for `k8s.io/client-go`, stop — the stdlib REST client in `k8s.go` is the design.
