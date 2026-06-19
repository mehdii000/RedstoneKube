package main

import (
	"encoding/json"
	"net/http"
	"time"
)

// handleStream pushes a fresh snapshot on connect and every 2s after.
// ponytail: a per-connection ticker, not a broadcast hub — fine for a few
// dashboard viewers. Add a fan-out hub only if many clients connect at once.
// Push cadence == scrape cadence; event-driven push on a k8s watch is a later upgrade.
func (c *Controller) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	send := func() {
		b, _ := json.Marshal(c.buildSnapshot())
		w.Write([]byte("data: "))
		w.Write(b)
		w.Write([]byte("\n\n"))
		flusher.Flush()
	}

	send()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			send()
		}
	}
}
