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
