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
