package main

import (
	"testing"
	"time"
)

func TestIdleReap(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	timeout := 5 * time.Minute

	pods := []Pod{
		{Name: "warm", Game: "stub", Alloc: false},      // pool baseline, empty
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
		"gone":        now.Add(-time.Hour),       // pod no longer exists
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
