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
