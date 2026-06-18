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
	created, allocated, deleted, unregistered []string
}) {
	rec := &struct {
		sync.Mutex
		created, allocated, deleted, unregistered []string
	}{}
	c := &Controller{
		games: []Game{{Name: "stub", Image: "mc/minigame-stub:dev", PoolSize: 1}},
		listPods: func(game string) ([]Pod, error) {
			if game == "" {
				return pods, nil
			}
			var out []Pod
			for _, p := range pods {
				if p.Game == game {
					out = append(out, p)
				}
			}
			return out, nil
		},
		createPod: func(name, game, image string) error {
			rec.Lock()
			rec.created = append(rec.created, name+"|"+game+"|"+image)
			rec.Unlock()
			return nil
		},
		setAllocated: func(n string) error {
			rec.Lock()
			rec.allocated = append(rec.allocated, n)
			rec.Unlock()
			return nil
		},
		deletePod: func(n string) error {
			rec.Lock()
			rec.deleted = append(rec.deleted, n)
			rec.Unlock()
			return nil
		},
		register: func(string, string) error { return nil },
		unregister: func(n string) error {
			rec.Lock()
			rec.unregistered = append(rec.unregistered, n)
			rec.Unlock()
			return nil
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

func TestAllocateUnknownGame404(t *testing.T) {
	c, _ := newTestController([]Pod{{Name: "mg-stub-1", Game: "stub", Ready: true, IP: "10.0.0.5"}})
	r := httptest.NewRequest("POST", "/allocate", strings.NewReader(`{"game":"ghost"}`))
	w := httptest.NewRecorder()
	c.handleAllocate(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", w.Code)
	}
}

func TestReconcileRefillsEachGame(t *testing.T) {
	// Two games configured, zero pods existing -> create one of each.
	c, rec := newTestController(nil)
	c.games = []Game{
		{Name: "stub", Image: "mc/minigame-stub:dev", PoolSize: 1},
		{Name: "parkour", Image: "mc/minigame-parkour:dev", PoolSize: 1},
	}
	c.reconcile()
	rec.Lock()
	defer rec.Unlock()
	if len(rec.created) != 2 {
		t.Fatalf("created = %v, want one per game", rec.created)
	}
	joined := strings.Join(rec.created, " ")
	if !strings.Contains(joined, "|stub|mc/minigame-stub:dev") ||
		!strings.Contains(joined, "|parkour|mc/minigame-parkour:dev") {
		t.Errorf("created with wrong game/image: %v", rec.created)
	}
}
