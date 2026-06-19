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
