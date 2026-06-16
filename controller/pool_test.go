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
