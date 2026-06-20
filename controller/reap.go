package main

import "time"

// idleReap returns the names of allocated instances that have reported 0 players,
// with fresh metrics, for at least `timeout`. Warm (unallocated) instances are never
// candidates, so the pool baseline is untouched. Stale/missing metrics never reap.
// It mutates emptySince: records first-seen-empty, clears pods that are no longer
// allocated-fresh-empty, and forgets pods that have vanished from `pods`.
func idleReap(pods []Pod, fresh func(name string) (Metrics, bool), emptySince map[string]time.Time, now time.Time, timeout time.Duration) []string {
	var reap []string
	live := make(map[string]bool, len(pods))
	for _, p := range pods {
		live[p.Name] = true
		m, ok := fresh(p.Name)
		if p.Alloc && ok && m.Players == 0 {
			if t, seen := emptySince[p.Name]; !seen {
				emptySince[p.Name] = now
			} else if now.Sub(t) >= timeout {
				reap = append(reap, p.Name)
				delete(emptySince, p.Name)
			}
		} else {
			delete(emptySince, p.Name)
		}
	}
	for name := range emptySince {
		if !live[name] {
			delete(emptySince, name)
		}
	}
	return reap
}
