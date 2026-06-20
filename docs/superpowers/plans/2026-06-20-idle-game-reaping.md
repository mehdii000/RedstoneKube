# Idle-Game Reaping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Automatically stop minigame instances that players have abandoned, without ever reaping the warm pool that keeps joins fast.

**Architecture:** A pure `idleReap` helper decides which instances to stop from the pod list + the metrics the controller already scrapes. `reconcile()` calls it each tick and tears reaped pods down exactly like `POST /done`. An instance is reaped only when it is allocated, its fresh metrics report 0 players, and it has been so for ≥ `IDLE_TIMEOUT`. Warm pool instances (`Alloc==false`) are never candidates.

**Tech Stack:** Go stdlib only (controller). No new dependency, no image rebuild.

## Global Constraints

- Controller is stdlib-only: no client-go, no external Go modules.
- The warm pool baseline (`poolSize` unallocated pods per game, maintained by `needed()`) must never be reaped.
- Stale or missing metrics ⇒ never reap (fail safe — can't confirm empty).
- Reuse the existing 2s `reconcile()` ticker and the existing `mu` lock; add no new goroutine.
- `metricsMaxAge` (const in `controller/snapshot.go`) is the freshness bound for metrics.

---

### Task 1: Pure `idleReap` helper

**Files:**
- Create: `controller/reap.go`
- Test: `controller/reap_test.go`

**Interfaces:**
- Consumes: `Pod` (controller/pool.go), `Metrics` (controller/metrics.go, has field `Players int`).
- Produces: `func idleReap(pods []Pod, fresh func(name string) (Metrics, bool), emptySince map[string]time.Time, now time.Time, timeout time.Duration) []string` — returns names to reap and mutates `emptySince` in place (records first-seen-empty, clears otherwise, forgets vanished pods).

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"testing"
	"time"
)

func TestIdleReap(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	timeout := 5 * time.Minute

	pods := []Pod{
		{Name: "warm", Game: "stub", Alloc: false},   // pool baseline, empty
		{Name: "fresh-alloc", Game: "stub", Alloc: true}, // just allocated, empty
		{Name: "stale-alloc", Game: "stub", Alloc: true}, // allocated, empty since long ago
		{Name: "busy", Game: "stub", Alloc: true},        // allocated, has players
		{Name: "nometrics", Game: "stub", Alloc: true},   // allocated, metrics unreachable
	}
	metrics := map[string]Metrics{
		"warm":        {Players: 0},
		"fresh-alloc": {Players: 0},
		"stale-alloc": {Players: 0},
		"busy":        {Players: 3},
		// "nometrics" intentionally absent -> fresh() returns false
	}
	fresh := func(n string) (Metrics, bool) { m, ok := metrics[n]; return m, ok }

	emptySince := map[string]time.Time{
		"stale-alloc": now.Add(-6 * time.Minute), // past timeout
		"gone":        now.Add(-time.Hour),        // pod no longer exists
	}

	reap := idleReap(pods, fresh, emptySince, now, timeout)

	if len(reap) != 1 || reap[0] != "stale-alloc" {
		t.Fatalf("reap = %v, want [stale-alloc]", reap)
	}
	if _, ok := emptySince["fresh-alloc"]; !ok {
		t.Errorf("fresh-alloc should be recorded in emptySince")
	}
	if _, ok := emptySince["warm"]; ok {
		t.Errorf("warm (unallocated) must never be tracked")
	}
	if _, ok := emptySince["busy"]; ok {
		t.Errorf("busy (players>0) must be cleared")
	}
	if _, ok := emptySince["nometrics"]; ok {
		t.Errorf("nometrics (stale) must not be tracked")
	}
	if _, ok := emptySince["stale-alloc"]; ok {
		t.Errorf("reaped pod must be removed from emptySince")
	}
	if _, ok := emptySince["gone"]; ok {
		t.Errorf("vanished pod must be forgotten")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd controller && go test ./... -run TestIdleReap`
Expected: FAIL — `undefined: idleReap`.

- [ ] **Step 3: Write minimal implementation**

```go
package main

import "time"

// idleReap returns the names of allocated instances that have reported 0 players,
// with fresh metrics, for at least `timeout`. Warm (unallocated) instances are never
// candidates, so the pool baseline is untouched. Stale/missing metrics never reap.
// It mutates emptySince: records first-seen-empty, clears pods that are no longer
// allocated-fresh-empty, and forgets pods that have vanished from `pods`.
func idleReap(pods []Pod, fresh func(name string) (Metrics, bool), emptySince map[string]time.Time, now time.Time, timeout time.Duration) []string {
	var reap []string
	live := make(map[string]bool, len(pods))
	for _, p := range pods {
		live[p.Name] = true
		m, ok := fresh(p.Name)
		if p.Alloc && ok && m.Players == 0 {
			if t, seen := emptySince[p.Name]; !seen {
				emptySince[p.Name] = now
			} else if now.Sub(t) >= timeout {
				reap = append(reap, p.Name)
				delete(emptySince, p.Name)
			}
		} else {
			delete(emptySince, p.Name)
		}
	}
	for name := range emptySince {
		if !live[name] {
			delete(emptySince, name)
		}
	}
	return reap
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd controller && go test ./... -run TestIdleReap`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add controller/reap.go controller/reap_test.go
git commit -m "feat(controller): idleReap pure helper for abandoned games"
```

---

### Task 2: Wire reaping into reconcile + config

**Files:**
- Modify: `controller/main.go` (add `emptySince map[string]time.Time` and `idleTimeout time.Duration` fields to `Controller`; reap block at end of `reconcile()`; init in `main()` from `IDLE_TIMEOUT` env)
- Modify: `controller/server_test.go` (add `emptySince` + `idleTimeout` to `newTestController`)
- Test: `controller/server_test.go` (new `TestReconcileReapsIdle`)

**Interfaces:**
- Consumes: `idleReap` (Task 1); `c.metrics.get(name, metricsMaxAge) (Metrics, bool)`; `metricsMaxAge` const (controller/snapshot.go); `c.unregister`, `c.deletePod`, `c.registered`.
- Produces: `Controller.emptySince map[string]time.Time`, `Controller.idleTimeout time.Duration`.

- [ ] **Step 1: Write the failing test**

Add to `controller/server_test.go`:

```go
func TestReconcileReapsIdle(t *testing.T) {
	c, rec := newTestController([]Pod{
		{Name: "mg-stub-busy", Game: "stub", Ready: true, Alloc: true},
		{Name: "mg-stub-idle", Game: "stub", Ready: true, Alloc: true},
	})
	c.registered["mg-stub-idle"] = true
	c.metrics.put("mg-stub-busy", Metrics{Players: 2})
	c.metrics.put("mg-stub-idle", Metrics{Players: 0})
	// idle pod first seen empty well past the timeout.
	c.emptySince["mg-stub-idle"] = time.Now().Add(-c.idleTimeout - time.Minute)

	c.reconcile()

	rec.Lock()
	defer rec.Unlock()
	if len(rec.deleted) != 1 || rec.deleted[0] != "mg-stub-idle" {
		t.Fatalf("deleted = %v, want [mg-stub-idle]", rec.deleted)
	}
	if len(rec.unregistered) != 1 || rec.unregistered[0] != "mg-stub-idle" {
		t.Fatalf("unregistered = %v, want [mg-stub-idle]", rec.unregistered)
	}
	if c.registered["mg-stub-idle"] {
		t.Errorf("reaped pod still registered")
	}
}
```

Note: this test needs `"time"` in `server_test.go`'s imports — add it.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd controller && go test ./... -run TestReconcileReapsIdle`
Expected: FAIL to compile — `c.emptySince`/`c.idleTimeout` undefined (and unused `time` import once added).

- [ ] **Step 3: Add the fields, the reap block, and the config**

In `controller/main.go`, add two fields to the `Controller` struct (after `podLogs`):

```go
	podLogs      func(name string, tail int) (string, error)

	emptySince  map[string]time.Time // name -> first seen allocated+empty
	idleTimeout time.Duration        // reap allocated+empty instances after this
```

At the **end** of `reconcile()` (after the registration-sync `for name := range c.registered` block, before the closing brace), add:

```go
	// reap abandoned (allocated, empty past idleTimeout) instances; warm pool untouched.
	fresh := func(n string) (Metrics, bool) { return c.metrics.get(n, metricsMaxAge) }
	for _, name := range idleReap(all, fresh, c.emptySince, time.Now(), c.idleTimeout) {
		if err := c.unregister(name); err != nil {
			log.Printf("reap: unregister %s: %v", name, err)
		}
		delete(c.registered, name)
		if err := c.deletePod(name); err != nil {
			log.Printf("reap: delete %s: %v", name, err)
		}
	}
```

In `main()`, inside the `c := &Controller{ ... }` literal, add the two fields:

```go
		podLogs:      k.podLogs,
		emptySince:   map[string]time.Time{},
		idleTimeout:  parseIdleTimeout(envOr("IDLE_TIMEOUT", "5m")),
```

And add the helper (next to `envOr`):

```go
// parseIdleTimeout reads IDLE_TIMEOUT (a Go duration); falls back to 5m on a bad value.
func parseIdleTimeout(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		log.Printf("IDLE_TIMEOUT %q invalid, using 5m", s)
		return 5 * time.Minute
	}
	return d
}
```

- [ ] **Step 4: Update the test controller**

In `controller/server_test.go`, add `"time"` to the import block, and add the two fields to the `Controller` literal in `newTestController` (after `podLogs`):

```go
		podLogs:    func(string, int) (string, error) { return "", nil },
		emptySince: map[string]time.Time{},
		idleTimeout: 5 * time.Minute,
```

- [ ] **Step 5: Run the full controller suite**

Run: `cd controller && go test ./...`
Expected: PASS (TestReconcileReapsIdle, TestIdleReap, and all existing tests).

- [ ] **Step 6: Commit**

```bash
git add controller/main.go controller/server_test.go
git commit -m "feat(controller): reap idle allocated games in reconcile (IDLE_TIMEOUT)"
```

---

## Self-Review

- **Spec coverage:** reapable signal (alloc + fresh + 0 players + ≥timeout) → Task 1 `idleReap`. Fail-safe on stale → Task 1 (`nometrics` case). Warm pool untouched → Task 1 (`warm` case). Teardown = `/done` path → Task 2 reap block. `IDLE_TIMEOUT` env default 5m → Task 2 `parseIdleTimeout`. Folds into existing reconcile, no new ticker → Task 2. All covered.
- **Placeholder scan:** none — every step has full code/commands.
- **Type consistency:** `idleReap` signature identical in Task 1 def and Task 2 call site; `emptySince`/`idleTimeout` field names consistent across main.go, server_test.go; `metricsMaxAge` and `c.metrics.get` match controller/snapshot.go + metrics.go.
