# Slice 3 — Metrics / Observability Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A live read-only dashboard over the platform showing pool counts, startup times, per-instance TPS/health, lifecycle state+reason, and tail logs — pushed to the browser via SSE.

**Architecture:** A new `mc-metrics` Paper plugin baked into `mc-base` exposes `GET :9100/metrics` JSON on every backend. The Go controller scrapes those endpoints on a ticker, derives lifecycle state+reason and startup time from the k8s pod object, and serves `GET /snapshot` (one-shot), `GET /stream` (SSE push), `GET /instances/{id}/logs` (k8s log proxy), and `GET /ui/` (an embedded single-page dashboard). Everything controller-side is in-memory.

**Tech Stack:** Go stdlib only (`net/http`, `//go:embed`, `com.sun.net.httpserver` on the Java side). Browser: Alpine.js + uPlot from CDN, native `EventSource`. No new Go or Gradle dependencies.

## Global Constraints

- Go: stdlib only — **no new module dependencies** (`go.mod` stays dependency-free; the controller Dockerfile copies no `go.sum`).
- Java plugins: `compileOnly 'io.papermc.paper:paper-api:1.21.8-R0.1-SNAPSHOT'`, Java toolchain 21, JUnit 5.10.2. No runtime deps (use the JDK's `com.sun.net.httpserver`, like `velocity-register`).
- All k8s resources in namespace `mc`. Pod truth lives in k8s; the UI reads only through the controller.
- Metrics endpoint is **read-only, unauthenticated, cluster-internal** (pod-IP only). Never add a write/command path in this slice.
- Keep non-Bukkit logic in plain classes so it unit-tests without paper-api (see `ServersHandler`, `Course`).
- Mark deliberate shortcuts with a `ponytail:` comment naming what's skipped and the upgrade path.
- Metrics port is **9100** everywhere (plugin bind + controller scrape).

---

### Task 1: Extend the controller's Pod view with lifecycle/startup fields

**Files:**
- Modify: `controller/pool.go` (the `Pod` struct)
- Modify: `controller/k8s.go` (`parsePodList`)
- Test: `controller/k8s_test.go`

**Interfaces:**
- Produces: `Pod` gains `Created time.Time`, `ReadyAt time.Time`, `Deleting bool`, `Restarts int`, `WaitReason, WaitMsg, TermReason, TermMsg string`, `TermExit int`. `parsePodList([]byte) ([]Pod, error)` now fills them.

- [ ] **Step 1: Write the failing test** — append to `controller/k8s_test.go`:

```go
func TestParsePodListLifecycleFields(t *testing.T) {
	body := []byte(`{"items":[{
	  "metadata":{"name":"mg-parkour-1","labels":{"game":"parkour","alloc":"true"},
	    "creationTimestamp":"2026-06-19T10:00:00Z","deletionTimestamp":"2026-06-19T10:05:00Z"},
	  "status":{"podIP":"10.0.0.5",
	    "conditions":[{"type":"Ready","status":"True","lastTransitionTime":"2026-06-19T10:00:07Z"}],
	    "containerStatuses":[{"restartCount":3,
	      "state":{"waiting":{"reason":"CrashLoopBackOff","message":"back-off restarting"}},
	      "lastState":{"terminated":{"reason":"Error","exitCode":1,"message":"boom"}}}]}}]}`)
	pods, err := parsePodList(body)
	if err != nil {
		t.Fatal(err)
	}
	p := pods[0]
	if p.Created.IsZero() || p.ReadyAt.IsZero() {
		t.Fatalf("timestamps not parsed: created=%v readyAt=%v", p.Created, p.ReadyAt)
	}
	if got := p.ReadyAt.Sub(p.Created).Seconds(); got != 7 {
		t.Errorf("startup = %v, want 7", got)
	}
	if !p.Deleting {
		t.Error("Deleting should be true")
	}
	if p.Restarts != 3 || p.WaitReason != "CrashLoopBackOff" {
		t.Errorf("restarts=%d waitReason=%q", p.Restarts, p.WaitReason)
	}
	if p.TermReason != "Error" || p.TermExit != 1 {
		t.Errorf("term=%q exit=%d", p.TermReason, p.TermExit)
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

Run: `cd controller && go test ./... -run TestParsePodListLifecycleFields`
Expected: FAIL — `p.Created undefined` (compile error).

- [ ] **Step 3: Add fields to `Pod`** in `controller/pool.go` (and `import "time"`):

```go
import "time"

// Pod is the controller's view of a minigame pod (subset of k8s pod state).
type Pod struct {
	Name, Game, IP string
	Ready, Alloc   bool

	// Slice 3: lifecycle + startup, all derived from the k8s pod object.
	Created    time.Time // metadata.creationTimestamp
	ReadyAt    time.Time // Ready condition lastTransitionTime (zero until Ready)
	Deleting   bool      // metadata.deletionTimestamp present
	Restarts   int       // containerStatuses[0].restartCount
	WaitReason string    // state.waiting.reason / lastState fallback
	WaitMsg    string
	TermReason string // lastState.terminated.reason
	TermMsg    string
	TermExit   int
}
```

- [ ] **Step 4: Fill them in `parsePodList`** in `controller/k8s.go`. Replace the anonymous struct and loop with:

```go
func parsePodList(body []byte) ([]Pod, error) {
	var pl struct {
		Items []struct {
			Metadata struct {
				Name              string            `json:"name"`
				Labels            map[string]string `json:"labels"`
				CreationTimestamp time.Time         `json:"creationTimestamp"`
				DeletionTimestamp *time.Time        `json:"deletionTimestamp"`
			} `json:"metadata"`
			Status struct {
				PodIP      string `json:"podIP"`
				Conditions []struct {
					Type, Status       string
					LastTransitionTime time.Time `json:"lastTransitionTime"`
				} `json:"conditions"`
				ContainerStatuses []struct {
					RestartCount int `json:"restartCount"`
					State        struct {
						Waiting    *struct{ Reason, Message string } `json:"waiting"`
						Terminated *struct {
							Reason, Message string
							ExitCode        int `json:"exitCode"`
						} `json:"terminated"`
					} `json:"state"`
					LastState struct {
						Terminated *struct {
							Reason, Message string
							ExitCode        int `json:"exitCode"`
						} `json:"terminated"`
					} `json:"lastState"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &pl); err != nil {
		return nil, err
	}
	out := make([]Pod, 0, len(pl.Items))
	for _, it := range pl.Items {
		p := Pod{
			Name:     it.Metadata.Name,
			Game:     it.Metadata.Labels["game"],
			IP:       it.Status.PodIP,
			Alloc:    it.Metadata.Labels["alloc"] == "true",
			Created:  it.Metadata.CreationTimestamp,
			Deleting: it.Metadata.DeletionTimestamp != nil,
		}
		for _, c := range it.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				p.Ready = true
				p.ReadyAt = c.LastTransitionTime
			}
		}
		if len(it.Status.ContainerStatuses) > 0 {
			cs := it.Status.ContainerStatuses[0]
			p.Restarts = cs.RestartCount
			if cs.State.Waiting != nil {
				p.WaitReason, p.WaitMsg = cs.State.Waiting.Reason, cs.State.Waiting.Message
			}
			if t := cs.LastState.Terminated; t != nil {
				p.TermReason, p.TermMsg, p.TermExit = t.Reason, t.Message, t.ExitCode
			} else if t := cs.State.Terminated; t != nil {
				p.TermReason, p.TermMsg, p.TermExit = t.Reason, t.Message, t.ExitCode
			}
		}
		out = append(out, p)
	}
	return out, nil
}
```

Add `"time"` to the `controller/k8s.go` imports.

- [ ] **Step 5: Run the test, verify it passes**

Run: `cd controller && go test ./...`
Expected: PASS (existing tests still green — fields are additive).

- [ ] **Step 6: Commit**

```bash
git add controller/pool.go controller/k8s.go controller/k8s_test.go
git commit -m "feat(controller): parse pod lifecycle + startup fields"
```

---

### Task 2: `lifecycle()` and `startupSeconds()` pure functions

**Files:**
- Create: `controller/lifecycle.go`
- Test: `controller/lifecycle_test.go`

**Interfaces:**
- Produces: `lifecycle(p Pod) (state, reason string)` returning state ∈ {booting, running, crashing, stopping}; `startupSeconds(p Pod) float64` (0 if not yet Ready).

- [ ] **Step 1: Write the failing test** — `controller/lifecycle_test.go`:

```go
package main

import (
	"testing"
	"time"
)

func TestLifecycle(t *testing.T) {
	cases := []struct {
		name      string
		pod       Pod
		wantState string
	}{
		{"terminating", Pod{Deleting: true}, "stopping"},
		{"crashloop", Pod{Restarts: 2, WaitReason: "CrashLoopBackOff"}, "crashing"},
		{"running", Pod{Ready: true}, "running"},
		{"booting", Pod{WaitReason: "ContainerCreating"}, "booting"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := lifecycle(c.pod)
			if got != c.wantState {
				t.Fatalf("state = %q, want %q", got, c.wantState)
			}
			if c.wantState != "running" && reason == "" {
				t.Errorf("expected a non-empty reason for %s", c.wantState)
			}
		})
	}
}

func TestStartupSeconds(t *testing.T) {
	base := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	p := Pod{Created: base, ReadyAt: base.Add(8 * time.Second), Ready: true}
	if got := startupSeconds(p); got != 8 {
		t.Errorf("startupSeconds = %v, want 8", got)
	}
	if got := startupSeconds(Pod{Created: base}); got != 0 {
		t.Errorf("not-ready startup = %v, want 0", got)
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

Run: `cd controller && go test ./... -run 'TestLifecycle|TestStartupSeconds'`
Expected: FAIL — `lifecycle` / `startupSeconds` undefined.

- [ ] **Step 3: Implement** `controller/lifecycle.go`:

```go
package main

import "fmt"

// lifecycle maps a k8s pod's observed state to one of four operational states
// plus a human reason. Pure: everything comes from the Pod fields parsePodList
// already filled. Order matters — deleting and crashing win over Ready.
func lifecycle(p Pod) (state, reason string) {
	switch {
	case p.Deleting:
		return "stopping", "Terminating"
	case p.Restarts > 0 || p.WaitReason == "CrashLoopBackOff":
		if p.WaitReason != "" {
			return "crashing", strings.TrimSpace(p.WaitReason + " " + p.WaitMsg)
		}
		if p.TermReason != "" {
			return "crashing", fmt.Sprintf("%s (exit %d) %s", p.TermReason, p.TermExit, p.TermMsg)
		}
		return "crashing", fmt.Sprintf("restarted %d time(s)", p.Restarts)
	case p.Ready:
		return "running", ""
	default:
		if p.WaitReason != "" {
			return "booting", strings.TrimSpace(p.WaitReason + " " + p.WaitMsg)
		}
		return "booting", "Pending"
	}
}

// startupSeconds is pod create -> Ready, derived entirely from k8s timestamps
// (no controller-side bookkeeping). 0 until the pod is Ready.
// ponytail: derived from the Ready condition's lastTransitionTime rather than
// the controller timing it; good enough and stateless. Time it in-controller
// only if k8s timestamps ever prove too coarse.
func startupSeconds(p Pod) float64 {
	if p.ReadyAt.IsZero() || p.Created.IsZero() {
		return 0
	}
	return p.ReadyAt.Sub(p.Created).Seconds()
}
```

Add `"strings"` to the imports (used above):

```go
import (
	"fmt"
	"strings"
)
```

- [ ] **Step 4: Run the test, verify it passes**

Run: `cd controller && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add controller/lifecycle.go controller/lifecycle_test.go
git commit -m "feat(controller): lifecycle state+reason and startup pure fns"
```

---

### Task 3: Metrics types, `parseMetrics`, and the scrape cache

**Files:**
- Create: `controller/metrics.go`
- Test: `controller/metrics_test.go`

**Interfaces:**
- Produces: `type Metrics struct { TPS, MSPT float64; Players, MaxPlayers int; UptimeSec float64; JvmStartupSec float64 }`; `parseMetrics([]byte) (Metrics, error)`; `type metricsCache struct { ... }` with `func newMetricsCache() *metricsCache`, `(*metricsCache) put(name string, m Metrics)`, and `(*metricsCache) get(name string, maxAge time.Duration) (Metrics, bool)` (bool = fresh).

- [ ] **Step 1: Write the failing test** — `controller/metrics_test.go`:

```go
package main

import (
	"testing"
	"time"
)

func TestParseMetrics(t *testing.T) {
	m, err := parseMetrics([]byte(`{"tps":19.98,"mspt":3.1,"players":2,"maxPlayers":20,"uptimeSec":412,"jvmStartupSec":6.2}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.TPS != 19.98 || m.Players != 2 || m.MaxPlayers != 20 || m.JvmStartupSec != 6.2 {
		t.Errorf("bad parse: %+v", m)
	}
}

func TestMetricsCacheFreshness(t *testing.T) {
	c := newMetricsCache()
	c.put("a", Metrics{TPS: 20})
	if _, ok := c.get("a", time.Second); !ok {
		t.Error("just-put entry should be fresh")
	}
	if _, ok := c.get("missing", time.Second); ok {
		t.Error("missing entry should not be fresh")
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

Run: `cd controller && go test ./... -run 'TestParseMetrics|TestMetricsCache'`
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Implement** `controller/metrics.go`:

```go
package main

import (
	"encoding/json"
	"sync"
	"time"
)

// Metrics is one backend's self-reported health (from its mc-metrics /metrics).
type Metrics struct {
	TPS           float64 `json:"tps"`
	MSPT          float64 `json:"mspt"`
	Players       int     `json:"players"`
	MaxPlayers    int     `json:"maxPlayers"`
	UptimeSec     float64 `json:"uptimeSec"`
	JvmStartupSec float64 `json:"jvmStartupSec"`
}

func parseMetrics(b []byte) (Metrics, error) {
	var m Metrics
	err := json.Unmarshal(b, &m)
	return m, err
}

// metricsCache holds the latest scrape per instance with its timestamp, so the
// snapshot can mark stale instances instead of showing lies.
// ponytail: a plain map under one lock; fine for a handful of pods.
type metricsCache struct {
	mu   sync.Mutex
	data map[string]struct {
		m  Metrics
		at time.Time
	}
}

func newMetricsCache() *metricsCache {
	return &metricsCache{data: map[string]struct {
		m  Metrics
		at time.Time
	}{}}
}

func (c *metricsCache) put(name string, m Metrics) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[name] = struct {
		m  Metrics
		at time.Time
	}{m, time.Now()}
}

// get returns the cached metrics and whether they are newer than maxAge.
func (c *metricsCache) get(name string, maxAge time.Duration) (Metrics, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data[name]
	if !ok || time.Since(e.at) > maxAge {
		return Metrics{}, false
	}
	return e.m, true
}
```

- [ ] **Step 4: Run the test, verify it passes**

Run: `cd controller && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add controller/metrics.go controller/metrics_test.go
git commit -m "feat(controller): metrics type, parser, and scrape cache"
```

---

### Task 4: Snapshot builder + `GET /snapshot`

**Files:**
- Create: `controller/snapshot.go`
- Modify: `controller/main.go` (add cache/allocFails fields, init, scrape ticker, route)
- Modify: `controller/server_test.go` (init new maps in `newTestController`)
- Test: `controller/snapshot_test.go`

**Interfaces:**
- Consumes: `lifecycle`, `startupSeconds` (Task 2), `Metrics`/`metricsCache` (Task 3), `Game`/`findGame` (existing), `Pod` (Task 1).
- Produces: `type Snapshot struct{ Games []GameStat; Instances []Instance }`; `(*Controller) buildSnapshot() Snapshot`; `(*Controller) handleSnapshot(http.ResponseWriter,*http.Request)`. `Controller` gains fields `metrics *metricsCache`, `allocFails map[string]int`, `fetchMetrics func(ip string) (Metrics, bool)`.

- [ ] **Step 1: Write the failing test** — `controller/snapshot_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBuildSnapshot(t *testing.T) {
	base := time.Now().Add(-time.Minute)
	c, _ := newTestController([]Pod{
		{Name: "mg-stub-1", Game: "stub", IP: "10.0.0.1", Ready: true,
			Created: base, ReadyAt: base.Add(6 * time.Second)},
		{Name: "mg-stub-2", Game: "stub", IP: "10.0.0.2", WaitReason: "ContainerCreating"},
	})
	c.metrics.put("mg-stub-1", Metrics{TPS: 19.9, Players: 1})

	snap := c.buildSnapshot()

	if len(snap.Instances) != 2 || len(snap.Games) != 1 {
		t.Fatalf("got %d instances, %d games", len(snap.Instances), len(snap.Games))
	}
	if snap.Games[0].Ready != 1 || snap.Games[0].Booting != 1 || snap.Games[0].Total != 2 {
		t.Errorf("game stat wrong: %+v", snap.Games[0])
	}
	var ready *Instance
	for i := range snap.Instances {
		if snap.Instances[i].ID == "mg-stub-1" {
			ready = &snap.Instances[i]
		}
	}
	if ready == nil || ready.Lifecycle.State != "running" || ready.Metrics == nil || ready.StartupSeconds != 6 {
		t.Errorf("ready instance wrong: %+v", ready)
	}
}

func TestHandleSnapshotJSON(t *testing.T) {
	c, _ := newTestController([]Pod{{Name: "mg-stub-1", Game: "stub", Ready: true}})
	rec := httptest.NewRecorder()
	c.handleSnapshot(rec, httptest.NewRequest("GET", "/snapshot", nil))
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	var snap Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

Run: `cd controller && go test ./... -run 'TestBuildSnapshot|TestHandleSnapshot'`
Expected: FAIL — `c.metrics`, `buildSnapshot`, `Snapshot`, `Instance` undefined.

- [ ] **Step 3: Add fields to `Controller`** in `controller/main.go` (inside the struct, after `registered`):

```go
	metrics      *metricsCache
	allocFails   map[string]int
	fetchMetrics func(ip string) (Metrics, bool) // injectable for tests
```

- [ ] **Step 4: Implement** `controller/snapshot.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"time"
)

const metricsMaxAge = 6 * time.Second // ~3 scrape ticks before "stale"

type GameStat struct {
	Name          string `json:"name"`
	PoolSize      int    `json:"poolSize"`
	Booting       int    `json:"booting"`
	Ready         int    `json:"ready"`
	Allocated     int    `json:"allocated"`
	Total         int    `json:"total"`
	Registered    int    `json:"registered"`
	AllocFailures int    `json:"allocFailures"`
}

type Lifecycle struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type Instance struct {
	ID             string    `json:"id"`
	Game           string    `json:"game"`
	Address        string    `json:"address"`
	Alloc          bool      `json:"alloc"`
	Lifecycle      Lifecycle `json:"lifecycle"`
	StartupSeconds float64   `json:"startupSeconds"`
	Metrics        *Metrics  `json:"metrics"` // nil when unreachable/stale
	Stale          bool      `json:"stale"`
}

type Snapshot struct {
	Games     []GameStat `json:"games"`
	Instances []Instance `json:"instances"`
}

// buildSnapshot reads current pods + the metrics cache into the wire shape the
// dashboard consumes. Pure read; safe to call from many SSE connections.
func (c *Controller) buildSnapshot() Snapshot {
	pods, err := c.listPods("")
	if err != nil {
		pods = nil // a transient k8s error shows an empty board, not a crash
	}
	stats := map[string]*GameStat{}
	for _, g := range c.games {
		stats[g.Name] = &GameStat{Name: g.Name, PoolSize: g.PoolSize}
	}

	c.mu.Lock()
	registered := make(map[string]bool, len(c.registered))
	for k, v := range c.registered {
		registered[k] = v
	}
	fails := map[string]int{}
	for k, v := range c.allocFails {
		fails[k] = v
	}
	c.mu.Unlock()

	insts := make([]Instance, 0, len(pods))
	for _, p := range pods {
		state, reason := lifecycle(p)
		inst := Instance{
			ID: p.Name, Game: p.Game, Address: p.IP + ":25565", Alloc: p.Alloc,
			Lifecycle: Lifecycle{state, reason}, StartupSeconds: startupSeconds(p),
		}
		if m, fresh := c.metrics.get(p.Name, metricsMaxAge); fresh {
			mm := m
			inst.Metrics = &mm
		} else {
			inst.Stale = true
		}
		insts = append(insts, inst)

		gs := stats[p.Game]
		if gs == nil {
			continue
		}
		gs.Total++
		switch {
		case p.Alloc:
			gs.Allocated++
		case state == "running":
			gs.Ready++
		default:
			gs.Booting++
		}
		if registered[p.Name] {
			gs.Registered++
		}
	}

	games := make([]GameStat, 0, len(c.games))
	for _, g := range c.games {
		gs := stats[g.Name]
		gs.AllocFailures = fails[g.Name]
		games = append(games, *gs)
	}
	return Snapshot{Games: games, Instances: insts}
}

func (c *Controller) handleSnapshot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(c.buildSnapshot())
}
```

- [ ] **Step 5: Init the new fields in `newTestController`** — in `controller/server_test.go`, add to the `Controller{...}` literal (after `registered: map[string]bool{},`):

```go
		metrics:    newMetricsCache(),
		allocFails: map[string]int{},
```

- [ ] **Step 6: Run the tests, verify they pass**

Run: `cd controller && go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add controller/snapshot.go controller/main.go controller/server_test.go controller/snapshot_test.go
git commit -m "feat(controller): snapshot builder + GET /snapshot"
```

---

### Task 5: Scrape ticker + wire snapshot route in `main`

**Files:**
- Modify: `controller/main.go` (default `fetchMetrics`, scrape func, init maps, ticker, route)

**Interfaces:**
- Consumes: `metricsCache`, `parseMetrics`, `buildSnapshot`.
- Produces: `(*Controller) scrape()` (one pass over Ready pods); real `fetchMetrics` over HTTP. No new test — exercised manually + via existing snapshot tests with the injected fake.

- [ ] **Step 1: Add the scrape method** — append to `controller/main.go`:

```go
// scrape pulls /metrics from every Ready pod (short timeout) into the cache.
// Runs on the same cadence as reconcile; failures just leave the entry stale.
func (c *Controller) scrape() {
	pods, err := c.listPods("")
	if err != nil {
		log.Printf("scrape: list: %v", err)
		return
	}
	for _, p := range pods {
		if p.IP == "" {
			continue
		}
		if m, ok := c.fetchMetrics(p.IP); ok {
			c.metrics.put(p.Name, m)
		}
	}
}

// httpFetchMetrics is the production fetchMetrics: GET http://<ip>:9100/metrics.
func httpFetchMetrics(hc *http.Client) func(string) (Metrics, bool) {
	return func(ip string) (Metrics, bool) {
		resp, err := hc.Get("http://" + ip + ":9100/metrics")
		if err != nil {
			return Metrics{}, false
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return Metrics{}, false
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return Metrics{}, false
		}
		m, err := parseMetrics(b)
		if err != nil {
			return Metrics{}, false
		}
		return m, true
	}
}
```

Add `"io"` to `controller/main.go` imports.

- [ ] **Step 2: Init the new fields + scrape ticker in `main()`** — in the `c := &Controller{...}` literal add:

```go
		metrics:    newMetricsCache(),
		allocFails: map[string]int{},
```

Immediately after building `c`, set the fetcher and start the scrape ticker (place near the existing reconcile goroutine):

```go
	c.fetchMetrics = httpFetchMetrics(&http.Client{Timeout: 2 * time.Second})

	go func() {
		for range time.Tick(2 * time.Second) {
			c.scrape()
		}
	}()
```

- [ ] **Step 3: Register the snapshot route** — in `main()` next to the other `http.HandleFunc` calls:

```go
	http.HandleFunc("/snapshot", c.handleSnapshot)
```

- [ ] **Step 4: Build + test**

Run: `cd controller && go build ./... && go test ./...`
Expected: build OK, tests PASS.

- [ ] **Step 5: Commit**

```bash
git add controller/main.go
git commit -m "feat(controller): scrape ticker + /snapshot route"
```

---

### Task 6: `GET /stream` SSE push

**Files:**
- Create: `controller/stream.go`
- Modify: `controller/main.go` (route)
- Test: `controller/stream_test.go`

**Interfaces:**
- Consumes: `buildSnapshot`.
- Produces: `(*Controller) handleStream(http.ResponseWriter, *http.Request)`.

- [ ] **Step 1: Write the failing test** — `controller/stream_test.go`:

```go
package main

import (
	"bufio"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamSendsInitialSnapshot(t *testing.T) {
	c, _ := newTestController([]Pod{{Name: "mg-stub-1", Game: "stub", Ready: true}})
	srv := httptest.NewServer(http.HandlerFunc(c.handleStream))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
	// Read the first SSE data line, then bail — the stream is infinite.
	done := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data:") {
				done <- line
				return
			}
		}
		done <- ""
	}()
	select {
	case line := <-done:
		if !strings.Contains(line, "\"instances\"") {
			t.Errorf("first event missing snapshot: %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no SSE event within 2s")
	}
}
```

Add `"net/http"` to the test imports.

- [ ] **Step 2: Run it, verify it fails**

Run: `cd controller && go test ./... -run TestStreamSendsInitialSnapshot`
Expected: FAIL — `handleStream` undefined.

- [ ] **Step 3: Implement** `controller/stream.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"time"
)

// handleStream pushes a fresh snapshot on connect and every 2s after.
// ponytail: a per-connection ticker, not a broadcast hub — fine for a few
// dashboard viewers. Add a fan-out hub only if many clients connect at once.
// Push cadence == scrape cadence; event-driven push on a k8s watch is a later upgrade.
func (c *Controller) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	send := func() {
		b, _ := json.Marshal(c.buildSnapshot())
		w.Write([]byte("data: "))
		w.Write(b)
		w.Write([]byte("\n\n"))
		flusher.Flush()
	}

	send()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			send()
		}
	}
}
```

- [ ] **Step 4: Register the route** — in `controller/main.go` `main()`:

```go
	http.HandleFunc("/stream", c.handleStream)
```

- [ ] **Step 5: Run the test, verify it passes**

Run: `cd controller && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add controller/stream.go controller/main.go controller/stream_test.go
git commit -m "feat(controller): SSE /stream push"
```

---

### Task 7: Log proxy — `GET /instances/{id}/logs`

**Files:**
- Modify: `controller/k8s.go` (`podLogs`)
- Modify: `controller/main.go` (`Controller.podLogs` field, init, dispatch `/instances/` → done|logs)
- Modify: `controller/server_test.go` (init `podLogs` in `newTestController`)
- Test: `controller/server_test.go` (new test)

**Interfaces:**
- Consumes: `kube.do` (existing).
- Produces: `(*kube) podLogs(name string, tail int) (string, error)`; `Controller.podLogs func(name string, tail int) (string, error)`; `(*Controller) handleInstances` routing `/done` vs `/logs`; `(*Controller) handleLogs`.

- [ ] **Step 1: Write the failing test** — append to `controller/server_test.go`:

```go
func TestHandleLogs(t *testing.T) {
	c, _ := newTestController([]Pod{{Name: "mg-stub-1", Game: "stub"}})
	c.podLogs = func(name string, tail int) (string, error) {
		if name != "mg-stub-1" || tail != 50 {
			t.Fatalf("podLogs(%q,%d)", name, tail)
		}
		return "line1\nline2\n", nil
	}
	rec := httptest.NewRecorder()
	c.handleInstances(rec, httptest.NewRequest("GET", "/instances/mg-stub-1/logs?tail=50", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "line2") {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

Run: `cd controller && go test ./... -run TestHandleLogs`
Expected: FAIL — `c.podLogs` / `handleInstances` undefined.

- [ ] **Step 3: Add `podLogs` to `kube`** in `controller/k8s.go`:

```go
// podLogs returns the last `tail` lines of a pod's log via the SA token.
func (k *kube) podLogs(name string, tail int) (string, error) {
	b, code, err := k.do("GET",
		fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/log?tailLines=%d", namespace, name, tail),
		"", "")
	if err != nil {
		return "", err
	}
	if code != 200 {
		return "", fmt.Errorf("podLogs %s: %d %s", name, code, b)
	}
	return string(b), nil
}
```

- [ ] **Step 4: Add the field + dispatcher.** In `controller/main.go`, add to the `Controller` struct:

```go
	podLogs func(name string, tail int) (string, error)
```

Replace the `http.HandleFunc("/instances/", c.handleDone)` registration with `c.handleInstances`, and add the dispatcher + log handler:

```go
// handleInstances routes /instances/{id}/{done|logs}.
func (c *Controller) handleInstances(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/instances/")
	switch {
	case strings.HasSuffix(rest, "/done"):
		c.handleDone(w, r)
	case strings.HasSuffix(rest, "/logs"):
		c.handleLogs(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (c *Controller) handleLogs(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/instances/"), "/logs")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "bad instance id", http.StatusBadRequest)
		return
	}
	tail := 200
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tail = n
		}
	}
	out, err := c.podLogs(id, tail)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(out))
}
```

Add `"strconv"` to `controller/main.go` imports. Update the route line:

```go
	http.HandleFunc("/instances/", c.handleInstances)
```

Wire the real `podLogs` where `c` is built in `main()`:

```go
		podLogs: k.podLogs,
```

- [ ] **Step 5: Init `podLogs` in the test helper** — in `controller/server_test.go` `newTestController`, add to the literal:

```go
		podLogs: func(string, int) (string, error) { return "", nil },
```

- [ ] **Step 6: Run the tests, verify they pass**

Run: `cd controller && go test ./...`
Expected: PASS (the existing `/done` tests still route through `handleInstances`).

- [ ] **Step 7: Commit**

```bash
git add controller/k8s.go controller/main.go controller/server_test.go
git commit -m "feat(controller): tail-N pod log proxy + /instances dispatcher"
```

---

### Task 8: Count alloc failures (503s)

**Files:**
- Modify: `controller/main.go` (`handleAllocate`)
- Test: `controller/server_test.go`

**Interfaces:**
- Consumes: `Controller.allocFails`.
- Produces: `allocFails[game]` increments on a 503 from `/allocate`.

- [ ] **Step 1: Write the failing test** — append to `controller/server_test.go`:

```go
func TestAllocFailureCounted(t *testing.T) {
	c, _ := newTestController(nil) // no pods => no allocatable => 503
	rec := httptest.NewRecorder()
	c.handleAllocate(rec, httptest.NewRequest("POST", "/allocate", strings.NewReader(`{"game":"stub"}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", rec.Code)
	}
	if c.allocFails["stub"] != 1 {
		t.Errorf("allocFails[stub] = %d, want 1", c.allocFails["stub"])
	}
}
```

Add `"net/http"` to the test imports if not already present.

- [ ] **Step 2: Run it, verify it fails**

Run: `cd controller && go test ./... -run TestAllocFailureCounted`
Expected: FAIL — counter stays 0.

- [ ] **Step 3: Increment on 503** — in `controller/main.go` `handleAllocate`, replace the `p == nil` branch:

```go
	p := pickAllocatable(pods, body.Game)
	if p == nil {
		c.allocFails[body.Game]++
		http.Error(w, "no ready instance", http.StatusServiceUnavailable)
		return
	}
```

(The handler already holds `c.mu`, so the map write is safe.)

- [ ] **Step 4: Run the test, verify it passes**

Run: `cd controller && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add controller/main.go controller/server_test.go
git commit -m "feat(controller): count allocate 503s per game"
```

---

### Task 9: Embed and serve the dashboard page

**Files:**
- Create: `controller/index.html`
- Create: `controller/ui.go`
- Modify: `controller/main.go` (route)
- Test: `controller/ui_test.go`

**Interfaces:**
- Produces: `(*Controller) handleUI(http.ResponseWriter, *http.Request)` serving the embedded page at `/ui/`.

This task gets a **frontend-design pass** during implementation — the markup below is a working baseline (real SSE wiring, real data binding); refine the visual design (typography, color, layout, the sparkline) so it doesn't read as a default Bootstrap-y template. Keep it a single file with CDN scripts and no build step.

- [ ] **Step 1: Write the failing test** — `controller/ui_test.go`:

```go
package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleUIServesPage(t *testing.T) {
	c, _ := newTestController(nil)
	rec := httptest.NewRecorder()
	c.handleUI(rec, httptest.NewRequest("GET", "/ui/", nil))
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "EventSource") || !strings.Contains(body, "/stream") {
		t.Error("page should wire EventSource to /stream")
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

Run: `cd controller && go test ./... -run TestHandleUIServesPage`
Expected: FAIL — `handleUI` undefined.

- [ ] **Step 3: Create the page** `controller/index.html` (baseline — refine visuals in the frontend-design pass):

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>mc-platform — metrics</title>
<script defer src="https://cdn.jsdelivr.net/npm/alpinejs@3.x.x/dist/cdn.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/uplot@1.6.31/dist/uPlot.iife.min.js"></script>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/uplot@1.6.31/dist/uPlot.min.css">
<style>
  body { font-family: ui-monospace, monospace; margin: 1.5rem; background:#0f1115; color:#e6e6e6; }
  h1 { font-size: 1.1rem; letter-spacing:.04em; }
  .cards { display:flex; gap:1rem; flex-wrap:wrap; margin-bottom:1rem; }
  .card { background:#1a1d24; border:1px solid #2a2f3a; border-radius:8px; padding:.75rem 1rem; min-width:8rem; }
  .card b { display:block; font-size:1.6rem; }
  table { width:100%; border-collapse:collapse; font-size:.85rem; }
  th,td { text-align:left; padding:.35rem .5rem; border-bottom:1px solid #2a2f3a; }
  .badge { padding:.1rem .45rem; border-radius:999px; font-size:.75rem; }
  .running{background:#15401f;color:#7ee49a} .booting{background:#3a3413;color:#e6d27a}
  .crashing{background:#46161b;color:#ff8a93} .stopping{background:#2a2f3a;color:#9aa3b2}
  tr.sel { outline:1px solid #3b82f6; }
  pre { background:#0a0c10; padding:.75rem; border-radius:8px; overflow:auto; max-height:18rem; }
</style>
</head>
<body x-data="dash()">
  <h1>mc-platform · live metrics <span x-show="!connected" style="color:#ff8a93">(disconnected)</span></h1>

  <div class="cards">
    <div class="card"><span>instances</span><b x-text="snap.instances.length"></b></div>
    <div class="card"><span>players</span><b x-text="totalPlayers()"></b></div>
    <div class="card"><span>cluster TPS</span><b x-text="clusterTps()"></b></div>
  </div>

  <template x-for="g in snap.games" :key="g.name">
    <div class="card" style="display:inline-block;margin-right:.75rem">
      <span x-text="g.name"></span>
      <b x-text="`${g.ready}/${g.poolSize}`"></b>
      <small x-text="`booting ${g.booting} · alloc ${g.allocated} · 503s ${g.allocFailures}`"></small>
    </div>
  </template>

  <table>
    <thead><tr><th>instance</th><th>game</th><th>state</th><th>startup</th><th>TPS</th><th>MSPT</th><th>players</th></tr></thead>
    <tbody>
      <template x-for="i in snap.instances" :key="i.id">
        <tr :class="{sel: sel===i.id}" @click="select(i.id)">
          <td x-text="i.id"></td>
          <td x-text="i.game"></td>
          <td><span class="badge" :class="i.lifecycle.state" x-text="i.lifecycle.state"></span>
              <small x-text="i.lifecycle.reason"></small></td>
          <td x-text="i.startupSeconds ? i.startupSeconds.toFixed(1)+'s' : '—'"></td>
          <td x-text="i.metrics ? i.metrics.tps.toFixed(2) : (i.stale?'stale':'—')"></td>
          <td x-text="i.metrics ? i.metrics.mspt.toFixed(1) : '—'"></td>
          <td x-text="i.metrics ? i.metrics.players : '—'"></td>
        </tr>
      </template>
    </tbody>
  </table>

  <div x-show="sel" style="margin-top:1rem">
    <h1 x-text="`logs · ${sel}`"></h1>
    <div id="spark" style="margin:.5rem 0"></div>
    <pre x-text="logs"></pre>
  </div>

<script>
function dash() {
  return {
    snap: { games: [], instances: [] },
    connected: false,
    sel: null,
    logs: '',
    tps: [[],[]], // [time, value] for uPlot
    plot: null,
    init() {
      const es = new EventSource('/stream');
      es.onmessage = e => { this.snap = JSON.parse(e.data); this.connected = true; this.pushSpark(); };
      es.onerror = () => { this.connected = false; };
    },
    totalPlayers() { return this.snap.instances.reduce((s,i)=> s + (i.metrics?i.metrics.players:0), 0); },
    clusterTps() {
      const live = this.snap.instances.filter(i=>i.metrics);
      if (!live.length) return '—';
      return (live.reduce((s,i)=>s+i.metrics.tps,0)/live.length).toFixed(2);
    },
    select(id) {
      this.sel = id; this.tps = [[],[]];
      fetch(`/instances/${id}/logs?tail=200`).then(r=>r.text()).then(t=> this.logs = t);
    },
    pushSpark() {
      if (!this.sel) return;
      const inst = this.snap.instances.find(i=>i.id===this.sel);
      if (!inst || !inst.metrics) return;
      const t = Date.now()/1000;
      this.tps[0].push(t); this.tps[1].push(inst.metrics.tps);
      if (this.tps[0].length > 60) { this.tps[0].shift(); this.tps[1].shift(); }
      if (!this.plot) {
        this.plot = new uPlot({ width:420, height:120, scales:{x:{time:true}},
          series:[{}, {label:'TPS', stroke:'#7ee49a'}] }, this.tps, document.getElementById('spark'));
      } else { this.plot.setData(this.tps); }
    },
  };
}
</script>
</body>
</html>
```

- [ ] **Step 4: Embed + serve** `controller/ui.go`:

```go
package main

import (
	_ "embed"
	"net/http"
)

//go:embed index.html
var indexHTML []byte

func (c *Controller) handleUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}
```

- [ ] **Step 5: Register the route** — in `controller/main.go` `main()`:

```go
	http.HandleFunc("/ui/", c.handleUI)
```

- [ ] **Step 6: Run the test, verify it passes**

Run: `cd controller && go test ./...`
Expected: PASS.

- [ ] **Step 7: Frontend-design pass** — invoke the `frontend-design` skill and refine `index.html` visuals (type scale, color, spacing, state badges, sparkline) without changing the data wiring (`/stream`, `/snapshot`, `/instances/{id}/logs`). Re-run `go test ./...` (the UI test only checks the EventSource wiring, so visual changes stay green). Commit.

- [ ] **Step 8: Commit**

```bash
git add controller/index.html controller/ui.go controller/main.go controller/ui_test.go
git commit -m "feat(controller): embedded SSE dashboard page"
```

---

### Task 10: `mc-metrics` Paper plugin

**Files:**
- Create: `plugins/mc-metrics/build.gradle`
- Create: `plugins/mc-metrics/src/main/resources/plugin.yml`
- Create: `plugins/mc-metrics/src/main/java/mc/metrics/MetricsJson.java`
- Create: `plugins/mc-metrics/src/main/java/mc/metrics/MetricsPlugin.java`
- Test: `plugins/mc-metrics/src/test/java/mc/metrics/MetricsJsonTest.java`

**Interfaces:**
- Produces: `MetricsJson.build(double tps, double mspt, int players, int maxPlayers, long uptimeSec, double jvmStartupSec) -> String` (pure JSON, matches the controller's `parseMetrics` shape). `MetricsPlugin` serves `GET :9100/metrics`.

- [ ] **Step 1: build.gradle** `plugins/mc-metrics/build.gradle` (mirror `stub-game`):

```gradle
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
  testRuntimeOnly 'org.junit.platform:junit-platform-launcher'
}
test { useJUnitPlatform() }
jar { archiveFileName = 'metrics-plugin.jar' }
```

- [ ] **Step 2: plugin.yml** `plugins/mc-metrics/src/main/resources/plugin.yml`:

```yaml
name: MetricsExporter
version: 0.1.0
main: mc.metrics.MetricsPlugin
api-version: '1.21'
```

- [ ] **Step 3: Write the failing test** `plugins/mc-metrics/src/test/java/mc/metrics/MetricsJsonTest.java`:

```java
package mc.metrics;

import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.*;

class MetricsJsonTest {
  @Test void buildsParseableJson() {
    String s = MetricsJson.build(19.987, 3.14, 2, 20, 412, 6.25);
    assertTrue(s.contains("\"tps\":19.99"), s);   // 2dp, ROOT locale dot
    assertTrue(s.contains("\"players\":2"), s);
    assertTrue(s.contains("\"maxPlayers\":20"), s);
    assertTrue(s.contains("\"uptimeSec\":412"), s);
    assertTrue(s.contains("\"jvmStartupSec\":6.2"), s);
  }
}
```

- [ ] **Step 4: Run it, verify it fails**

Run: `docker run --rm -u $(id -u):$(id -g) -e GRADLE_USER_HOME=/work/.gradle -v $PWD/plugins/mc-metrics:/work -w /work gradle:jdk21 gradle test`
Expected: FAIL — `MetricsJson` does not exist (compile error).

- [ ] **Step 5: Implement** `plugins/mc-metrics/src/main/java/mc/metrics/MetricsJson.java`:

```java
package mc.metrics;

import java.util.Locale;

/** Pure JSON builder — no Bukkit, so it unit-tests without paper-api. */
public final class MetricsJson {
  private MetricsJson() {}

  public static String build(double tps, double mspt, int players, int maxPlayers,
                             long uptimeSec, double jvmStartupSec) {
    return String.format(Locale.ROOT,
      "{\"tps\":%.2f,\"mspt\":%.2f,\"players\":%d,\"maxPlayers\":%d,\"uptimeSec\":%d,\"jvmStartupSec\":%.1f}",
      tps, mspt, players, maxPlayers, uptimeSec, jvmStartupSec);
  }
}
```

- [ ] **Step 6: Implement** `plugins/mc-metrics/src/main/java/mc/metrics/MetricsPlugin.java`:

```java
package mc.metrics;

import com.sun.net.httpserver.HttpServer;
import org.bukkit.Bukkit;
import org.bukkit.plugin.java.JavaPlugin;
import java.io.IOException;
import java.io.OutputStream;
import java.lang.management.ManagementFactory;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;

public final class MetricsPlugin extends JavaPlugin {
  private HttpServer http;
  private long enableMillis;

  @Override public void onEnable() {
    enableMillis = System.currentTimeMillis();
    final double jvmStartupSec =
        (enableMillis - ManagementFactory.getRuntimeMXBean().getStartTime()) / 1000.0;
    try {
      http = HttpServer.create(new InetSocketAddress("0.0.0.0", 9100), 0);
    } catch (IOException e) {
      getLogger().severe("metrics server failed to bind 9100: " + e);
      return;
    }
    http.createContext("/metrics", ex -> {
      byte[] body;
      try {
        double tps = Bukkit.getTPS()[0];
        double mspt = Bukkit.getAverageTickTime();
        int players = Bukkit.getOnlinePlayers().size();
        int max = Bukkit.getMaxPlayers();
        long uptime = (System.currentTimeMillis() - enableMillis) / 1000;
        body = MetricsJson.build(tps, mspt, players, max, uptime, jvmStartupSec)
            .getBytes(StandardCharsets.UTF_8);
      } catch (Throwable t) {
        getLogger().warning("metrics render failed: " + t);
        ex.sendResponseHeaders(500, -1);
        ex.close();
        return;
      }
      ex.getResponseHeaders().set("Content-Type", "application/json");
      ex.sendResponseHeaders(200, body.length);
      try (OutputStream os = ex.getResponseBody()) { os.write(body); }
    });
    http.start();
    getLogger().info("metrics exporter on :9100");
  }

  @Override public void onDisable() {
    if (http != null) http.stop(0);
  }
}
```

- [ ] **Step 7: Run the test, verify it passes**

Run: `docker run --rm -u $(id -u):$(id -g) -e GRADLE_USER_HOME=/work/.gradle -v $PWD/plugins/mc-metrics:/work -w /work gradle:jdk21 gradle build`
Expected: BUILD SUCCESSFUL; `plugins/mc-metrics/build/libs/metrics-plugin.jar` exists.

- [ ] **Step 8: Commit**

```bash
git add plugins/mc-metrics/build.gradle plugins/mc-metrics/src
git commit -m "feat(mc-metrics): /metrics exporter plugin for mc-base"
```

---

### Task 11: Bake `mc-metrics` into `mc-base`; add RBAC for logs

**Files:**
- Modify: `images/mc-base/Dockerfile`
- Modify: `Makefile` (`build-base`, `.PHONY`)
- Modify: `deploy/k8s/controller.yaml` (Role: `pods/log`)

**Interfaces:** none (build/deploy wiring).

- [ ] **Step 1: Add the plugin COPY to `images/mc-base/Dockerfile`** — after the `ADD ${ASP_PLUGIN_URL}` line:

```dockerfile
COPY metrics-plugin.jar /server/plugins/metrics-plugin.jar
```

And include it in the existing `chmod 0644 ... && chown` of `/server/plugins` (it's already covered by `chown -R mc /server/plugins`; add the jar to the `chmod 0644` list):

```dockerfile
RUN chmod +x /entrypoint.sh && chmod 0644 /opt/server.jar /server/plugins/asp-plugin.jar /server/plugins/metrics-plugin.jar \
 && chown -R mc /server/plugins \
 && echo "eula=true" > /server/eula.txt && chown mc /server/eula.txt
```

- [ ] **Step 2: Build the plugin jar and copy it before `build-base`** — in `Makefile`, add a gradle runner + update `build-base`:

```makefile
METRICS_GRADLE := docker run --rm -u $(shell id -u):$(shell id -g) \
  -e GRADLE_USER_HOME=/work/.gradle \
  -v $(PWD)/plugins/mc-metrics:/work -w /work gradle:jdk21 gradle

build-base:
	$(METRICS_GRADLE) build
	cp plugins/mc-metrics/build/libs/metrics-plugin.jar images/mc-base/metrics-plugin.jar
	docker build -t mc/mc-base:dev \
	  --build-arg JRE_TAG=$(JRE_TAG) --build-arg ASP_URL=$(ASP_URL) \
	  --build-arg ASP_PLUGIN_URL=$(ASP_PLUGIN_URL) \
	  images/mc-base
	rm -f images/mc-base/metrics-plugin.jar
```

(`build-base` is already in `.PHONY`; no change needed there.)

- [ ] **Step 3: Add `pods/log` to the controller Role** in `deploy/k8s/controller.yaml` — add a rule under the existing `rules:`:

```yaml
  - apiGroups: [""]
    resources: ["pods/log"]
    verbs: ["get"]
```

- [ ] **Step 4: Build mc-base + a dependent game to prove the jar lands**

Run: `make build-base && make build-minigame-stub`
Expected: both builds succeed.

Verify the jar is in the image:
Run: `docker run --rm --entrypoint ls mc/mc-base:dev /server/plugins`
Expected: lists `asp-plugin.jar` and `metrics-plugin.jar`.

- [ ] **Step 5: Commit**

```bash
git add images/mc-base/Dockerfile Makefile deploy/k8s/controller.yaml
git commit -m "build: bake mc-metrics into mc-base + pods/log RBAC"
```

---

### Task 12: Live acceptance on kind + docs

**Files:**
- Modify: `AGENTS.md` (Slice 3 → DONE)
- Create: `docs/superpowers/SLICE-4-HANDOFF.md` (or update the handoff index per project convention)

**Interfaces:** none.

- [ ] **Step 1: Full rebuild + redeploy**

Run: `make up` (then Ctrl-C once the LoadBalancer IP is assigned), and `make apply` if the controller image changed without a cluster recreate. Confirm pods come up: `kubectl -n mc get pods`.

- [ ] **Step 2: Open the dashboard**

Run: `kubectl -n mc port-forward svc/controller 8080:8080`
Open `http://localhost:8080/ui/`. Verify:
- cluster cards populate; per-game cards show `ready/poolSize`, booting, alloc, 503s.
- instance table shows each pod with a **state badge + reason**, a **startup time**, and live **TPS/MSPT/players** (TPS ~20 on an idle backend).

- [ ] **Step 3: Exercise the observability paths**
- `curl -s localhost:8080/snapshot | jq` returns the same shape.
- Click a row → log panel fills (tail of that pod's log) and a TPS sparkline starts drawing.
- Allocate a game in-client (compass) → that instance flips to **allocated**, players climbs.
- `kubectl -n mc delete pod <one-instance>` → its row flips to **stopping/Terminating**, then a fresh booting pod appears (pool refill).
- Force a crash if practical (e.g. a bad image tag in a scratch ConfigMap entry) → row shows **crashing** with the waiting reason. (Optional — don't disrupt the working config to chase this.)

- [ ] **Step 4: Confirm the whole Go suite is green**

Run: `cd controller && go test ./...`
Expected: PASS.

- [ ] **Step 5: Update docs**
- `AGENTS.md`: mark **Slice 3 — DONE**, one line on what shipped (SSE metrics dashboard, `mc-metrics` plugin, controller `/snapshot` `/stream` `/instances/{id}/logs` `/ui/`, `pods/log` RBAC).
- Add a brief Slice 3 carryover/handoff note (new endpoints, port 9100, in-memory caches, deferred `POST /command`) following the existing handoff-doc convention.

- [ ] **Step 6: Commit**

```bash
git add AGENTS.md docs/superpowers/
git commit -m "docs(slice-3): mark done + handoff notes"
```

---

## Self-Review

**Spec coverage:**
- mc-metrics `/metrics` plugin → Task 10; baked into mc-base → Task 11. ✓
- Controller scrape + cache → Tasks 3, 5. ✓
- Startup timing → Task 1 (fields) + Task 2 (`startupSeconds`, derived from pod timestamps — a documented, lazier refinement of the spec's "controller records readyAt"). ✓
- Lifecycle state+reason pure fn → Task 2. ✓
- Alloc-failure counter → Task 8. ✓
- `GET /snapshot` → Task 4; `GET /stream` SSE → Task 6; `GET /instances/{id}/logs` + `pods/log` RBAC → Tasks 7, 11; `GET /ui/` embedded page → Task 9. ✓
- Alpine + uPlot, CDN, no build, EventSource, frontend-design pass → Task 9. ✓
- Tests: `lifecycle` (Task 2), `parseMetrics`/cache (Task 3), snapshot (Task 4), stream (Task 6), logs (Task 7), `MetricsJson` (Task 10). Manual acceptance (Task 12). ✓
- Out-of-scope (commands, Prometheus, follow-logs, history, mc-base behavior consolidation) — none implemented. ✓

**Placeholder scan:** no TBD/TODO; every code step has full code.

**Type consistency:** `Pod` fields (Task 1) consumed by `lifecycle`/`startupSeconds` (Task 2) and `buildSnapshot` (Task 4). `Metrics` JSON tags (Task 3) match `MetricsJson.build` output (Task 10) and the `/metrics` shape. `Controller` gains `metrics`, `allocFails`, `fetchMetrics` (Task 4/5), `podLogs` (Task 7) — all initialized in both `main()` and `newTestController`. Routes (`/snapshot`, `/stream`, `/instances/`, `/ui/`) registered once each.
