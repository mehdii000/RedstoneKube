package main

import (
	"encoding/json"
	"net/http"
	"time"
)

const metricsMaxAge = 6 * time.Second // ~3 scrape ticks before "stale"

type GameStat struct {
	Name          string `json:"name"`
	PoolSize      int    `json:"poolSize"`
	Booting       int    `json:"booting"`
	Ready         int    `json:"ready"`
	Allocated     int    `json:"allocated"`
	Total         int    `json:"total"`
	Registered    int    `json:"registered"`
	AllocFailures int    `json:"allocFailures"`
}

type Lifecycle struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type Instance struct {
	ID             string    `json:"id"`
	Game           string    `json:"game"`
	Address        string    `json:"address"`
	Alloc          bool      `json:"alloc"`
	Lifecycle      Lifecycle `json:"lifecycle"`
	StartupSeconds float64   `json:"startupSeconds"`
	Metrics        *Metrics  `json:"metrics"` // nil when unreachable/stale
	Stale          bool      `json:"stale"`
}

type Snapshot struct {
	Games     []GameStat `json:"games"`
	Instances []Instance `json:"instances"`
}

// buildSnapshot reads current pods + the metrics cache into the wire shape the
// dashboard consumes. Pure read; safe to call from many SSE connections.
func (c *Controller) buildSnapshot() Snapshot {
	pods, err := c.listPods("")
	if err != nil {
		pods = nil // a transient k8s error shows an empty board, not a crash
	}
	stats := map[string]*GameStat{}
	for _, g := range c.games {
		stats[g.Name] = &GameStat{Name: g.Name, PoolSize: g.PoolSize}
	}

	c.mu.Lock()
	registered := make(map[string]bool, len(c.registered))
	for k, v := range c.registered {
		registered[k] = v
	}
	fails := map[string]int{}
	for k, v := range c.allocFails {
		fails[k] = v
	}
	c.mu.Unlock()

	insts := make([]Instance, 0, len(pods))
	for _, p := range pods {
		state, reason := lifecycle(p)
		inst := Instance{
			ID: p.Name, Game: p.Game, Address: p.IP + ":25565", Alloc: p.Alloc,
			Lifecycle: Lifecycle{state, reason}, StartupSeconds: startupSeconds(p),
		}
		if m, fresh := c.metrics.get(p.Name, metricsMaxAge); fresh {
			mm := m
			inst.Metrics = &mm
		} else {
			inst.Stale = true
		}
		insts = append(insts, inst)

		gs := stats[p.Game]
		if gs == nil {
			continue
		}
		gs.Total++
		switch {
		case p.Alloc:
			gs.Allocated++
		case state == "running":
			gs.Ready++
		default:
			gs.Booting++
		}
		if registered[p.Name] {
			gs.Registered++
		}
	}

	games := make([]GameStat, 0, len(c.games))
	for _, g := range c.games {
		gs := stats[g.Name]
		gs.AllocFailures = fails[g.Name]
		games = append(games, *gs)
	}
	return Snapshot{Games: games, Instances: insts}
}

func (c *Controller) handleSnapshot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(c.buildSnapshot())
}
