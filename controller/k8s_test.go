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
