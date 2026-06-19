package main

import (
	"fmt"
	"strings"
)

// lifecycle maps a k8s pod's observed state to one of four operational states
// plus a human reason. Pure: everything comes from the Pod fields parsePodList
// already filled. Order matters — deleting and crashing win over Ready.
func lifecycle(p Pod) (state, reason string) {
	switch {
	case p.Deleting:
		return "stopping", "Terminating"
	case p.Restarts > 0 || p.WaitReason == "CrashLoopBackOff":
		if p.WaitReason != "" {
			return "crashing", strings.TrimSpace(p.WaitReason + " " + p.WaitMsg)
		}
		if p.TermReason != "" {
			return "crashing", fmt.Sprintf("%s (exit %d) %s", p.TermReason, p.TermExit, p.TermMsg)
		}
		return "crashing", fmt.Sprintf("restarted %d time(s)", p.Restarts)
	case p.Ready:
		return "running", ""
	default:
		if p.WaitReason != "" {
			return "booting", strings.TrimSpace(p.WaitReason + " " + p.WaitMsg)
		}
		return "booting", "Pending"
	}
}

// startupSeconds is pod create -> Ready, derived entirely from k8s timestamps
// (no controller-side bookkeeping). 0 until the pod is Ready.
// ponytail: derived from the Ready condition's lastTransitionTime rather than
// the controller timing it; good enough and stateless. Time it in-controller
// only if k8s timestamps ever prove too coarse.
func startupSeconds(p Pod) float64 {
	if p.ReadyAt.IsZero() || p.Created.IsZero() {
		return 0
	}
	return p.ReadyAt.Sub(p.Created).Seconds()
}
