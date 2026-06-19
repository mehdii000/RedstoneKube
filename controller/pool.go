package main

import "time"

// Pod is the controller's view of a minigame pod (subset of k8s pod state).
type Pod struct {
	Name, Game, IP string
	Ready, Alloc   bool

	// Slice 3: lifecycle + startup, all derived from the k8s pod object.
	Created    time.Time // metadata.creationTimestamp
	ReadyAt    time.Time // Ready condition lastTransitionTime (zero until Ready)
	Deleting   bool      // metadata.deletionTimestamp present
	Restarts   int       // containerStatuses[0].restartCount
	WaitReason string    // state.waiting.reason / lastState fallback
	WaitMsg    string
	TermReason string // lastState.terminated.reason
	TermMsg    string
	TermExit   int
}

// needed returns how many new pods to create to keep `desired` unallocated pods
// (Ready or still booting) in the pool. Allocated pods don't count — they belong
// to a game in progress. Deletes are not handled here; a finished pod removes
// itself via POST /done.
func needed(pods []Pod, desired int) int {
	free := 0
	for _, p := range pods {
		if !p.Alloc {
			free++
		}
	}
	if free >= desired {
		return 0
	}
	return desired - free
}

// pickAllocatable returns the first Ready, unallocated pod of the given game, or nil.
func pickAllocatable(pods []Pod, game string) *Pod {
	for i := range pods {
		p := &pods[i]
		if p.Game == game && p.Ready && !p.Alloc {
			return p
		}
	}
	return nil
}
