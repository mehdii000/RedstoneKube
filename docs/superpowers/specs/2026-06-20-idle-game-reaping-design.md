# Idle-game reaping (design)

Small addition to the Slice 3 branch: automatically stop minigame instances that
players have abandoned, **without** ever touching the warm pool that keeps joins fast.

## Problem

`needed(pods, poolSize)` (controller/pool.go) refills only **unallocated** pods, so each
game always has `poolSize` warm-but-empty instances ready to join. A naïve "stop empty
servers" reaper would kill those too. We want both: reap abandoned games, keep the warm pool.

The gap today: when a player is allocated an instance, joins, then leaves without the game
self-`/done`ing (only parkour auto-`/done`s on win), the pod stays `Alloc==true` forever,
holding a pod for nobody.

## Reapable signal

An instance is reaped only when **all** hold:

- `Alloc == true` — a player was sent there. Warm pool instances are `Alloc==false` and are
  never candidates, so the warm baseline is untouched.
- metrics are **fresh** (within `metricsMaxAge`) and report `players == 0`.
- it has been continuously allocated-and-empty for ≥ `IDLE_TIMEOUT` (default `5m`).

Stale or unreachable metrics ⇒ **not** reaped — we can't confirm it's empty, so we keep the
pod (fail safe). A just-allocated pod whose player is still connecting reports `players==0`
only until they join (seconds), well inside the timeout, then clears itself.

## Mechanism

- New `emptySince map[string]time.Time` field on `Controller`, guarded by the existing `mu`.
- Pure helper `idleReap(pods []Pod, fresh func(name string) (Metrics, bool), emptySince map[string]time.Time, now time.Time, timeout time.Duration) []string`
  returns the names to reap and mutates `emptySince` (records first-seen-empty, clears when a
  pod is no longer allocated-fresh-empty). Pure ⇒ unit-testable with no cluster.
- `reconcile()` (already holds `mu` and lists pods) calls `idleReap` before the refill loop and
  reaps each returned name with the same teardown as `POST /done`:
  `unregister` → `deletePod` → `delete(registered, name)` → `delete(emptySince, name)`.
- The next reconcile tick refills via `needed()`, reclaiming capacity and restoring the warm
  baseline automatically. No new ticker — folds into the existing 2s reconcile.

`fresh(name)` wraps `c.metrics.get(name, metricsMaxAge)` so the helper stays cluster-free.

## Config

`IDLE_TIMEOUT` env, parsed with `time.ParseDuration`, default `5m`. The reaper's calibration
knob — tune without a rebuild.

## Test

One unit test over `idleReap`:

- warm empty (`Alloc==false`) → not reaped
- allocated + empty, first seen now → not reaped, recorded in `emptySince`
- allocated + empty, recorded past timeout → reaped
- allocated + empty, recorded within timeout → kept
- allocated + players>0 → not reaped, cleared from `emptySince`
- allocated + stale/missing metrics → not reaped (and not recorded)

## Skipped (YAGNI)

- Per-game timeout overrides — one global env covers it; add to the `minigames` ConfigMap only
  if a game ever needs its own.
- A separate reaper ticker — reuse the existing reconcile cadence.
- No new dependency, no image rebuild (controller-only change).
