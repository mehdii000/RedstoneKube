# Slice 3 handoff (read this first in a fresh session)

Slice 3 shipped: a **detailed metrics / observability dashboard** over the platform, pushed live
to the browser over SSE. Verified end-to-end on `kind` (controller `/snapshot` shows pools +
scraped backend TPS/MSPT/startup; `/instances/{id}/logs`, `/stream`, `/ui/` all serve).

Spec: `docs/superpowers/specs/2026-06-19-slice-3-metrics-dashboard-design.md`.
Plan: `docs/superpowers/plans/2026-06-19-slice-3-metrics-dashboard.md`.

## What Slice 3 delivered

- **`mc-metrics` plugin** (`plugins/mc-metrics`) baked into **mc-base** → every `FROM mc/mc-base`
  backend serves `GET :9100/metrics` JSON (`tps`, `mspt`, `players`, `maxPlayers`, `uptimeSec`,
  `jvmStartupSec`) via the JDK `com.sun.net.httpserver`. Read-only, unauthenticated, cluster-internal.
- **Controller read model + endpoints** (all in-memory, stdlib only):
  - `GET /snapshot` → `{games:[{name,poolSize,booting,ready,allocated,total,registered,allocFailures}],
    instances:[{id,game,address,alloc,lifecycle{state,reason},startupSeconds,metrics|null,stale}]}`.
  - `GET /stream` → the same snapshot over **SSE** (`text/event-stream`), pushed on connect + every 2s.
  - `GET /instances/{id}/logs?tail=N` → proxies the k8s `pods/<id>/log` endpoint (needs `pods/log` RBAC).
  - `GET /ui/` → an embedded single-page dashboard (`//go:embed index.html`; Alpine.js + uPlot via CDN,
    native `EventSource`, no build step).
  - A scrape ticker (2s) pulls every Ready pod's `:9100/metrics` into a `metricsCache` (stale after 6s).
- **`lifecycle(pod) → {state,reason}`** pure fn maps k8s pod status → booting/running/crashing/stopping
  (`controller/lifecycle.go`, unit-tested). **`startupSeconds(pod)`** = create→Ready, derived from pod
  timestamps (stateless — no controller bookkeeping).
- Per-game **alloc-failure (503) counter**.
- **Idle-game reaping** (`controller/reap.go`): `reconcile()` reaps an instance that is **allocated**
  and has reported **0 players** with fresh metrics for ≥ `IDLE_TIMEOUT` (env, default `5m`). Warm
  pool instances (`Alloc==false`) are never candidates, so joins stay fast; stale metrics never reap.
  Reuses the existing reconcile tick + `mu`; teardown is identical to `POST /done`.

## Carryover facts (don't re-derive)

- **Metrics port is 9100** everywhere (plugin bind + controller scrape). Backends `EXPOSE 25565 9100`.
- Controller state added in Slice 3 is **in-memory** (metrics cache, alloc-failure counts) — lost on
  controller restart; that's fine for a dashboard, don't add a DB.
- Pod truth still lives in k8s; the UI reads **only through the controller** (`/snapshot` `/stream`),
  never k8s directly. Lifecycle + startup come from the pod object the controller already lists.
- View the dashboard: `kubectl -n mc port-forward svc/controller 8080:8080` → `http://localhost:8080/ui/`.
  The controller Service is ClusterIP (no LoadBalancer added — port-forward is the lazy access path).
- Existing endpoints unchanged: `POST /allocate`, `POST /instances/{id}/done`, `GET /healthz`. The
  `/instances/` route now dispatches `/done` vs `/logs` (`handleInstances`).

## Gotchas hit in Slice 3 (don't repeat)

- Java `String.format("%.1f", 6.25)` rounds HALF_UP → `6.3`, not `6.2`. Watch metric-formatting tests.
- `build-base` now builds + bakes `metrics-plugin.jar`; rebuilding any game (`build-minigame-*`,
  `build-lobby`) pulls the new mc-base automatically. After changing controller/plugin code on a live
  cluster: `make load && make apply && kubectl -n mc rollout restart deploy/controller` and recycle
  minigame pods (`kubectl -n mc delete pods -l app=minigame`) so they land on the new mc-base.

## Deferred (each its own future slice — don't pre-plan)

- **`POST /command`** into backends (read-WRITE console RCE) — needs bearer auth + allowlist + audit.
  Lazy home: extend the `mc-metrics` plugin with an authenticated `POST /command` →
  `Bukkit.dispatchCommand`; controller proxies it. Keep the auth — it's the security boundary.
- Prometheus/Grafana; live (follow) log streaming; server-side metric history; consolidating the
  per-game autosave/confine behavior into the shared mc-base plugin.

## Starting the next slice

1. Invoke `/ponytail`, then the brainstorming skill.
2. Pick the next increment from the roadmap in `AGENTS.md` (multi-replica Velocity sync, central world
   store, global persistence, or `POST /command`) based on a concrete need — not speculatively.
3. One slice per session: spec → plan → execute, `--no-ff` merge, keep the branch.
