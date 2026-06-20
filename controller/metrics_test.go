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
