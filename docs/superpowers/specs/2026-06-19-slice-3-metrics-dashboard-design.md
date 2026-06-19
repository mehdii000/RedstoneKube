# Slice 3 — Metrics / Observability Dashboard (design)

Status: approved, ready for implementation plan.
Scope decided with the user: **core observability, read-only**. `POST /command` (read-WRITE
console RCE) is deferred to its own later slice. No Prometheus/Grafana. No server-side metric
history. No live log streaming (tail-N snapshot only).

## Goal

A live dashboard over the platform so issues are easy to diagnose and the running state is always
visible. The user explicitly asked for **startup times, pool counts, and per-instance TPS/health**,
plus **lifecycle state + reason** and **logs in the UI**. Real-time via **SSE push**.

## Architecture (data flow)

```
backend pods (FROM mc-base)          controller (Go)                 browser
  mc-metrics plugin                                                  one embedded page
  GET :9100/metrics  <--scrape--  scrape ticker (~2s)
                                  + k8s pod list (already have)
                                  + startup timing (create->Ready)
                                  + lifecycle(pod) pure fn
                                  + 503 alloc counter
                                       |
                                  snapshot builder -> {games, instances}
                                       |                         |
                                  GET /snapshot (one-shot)   GET /stream (SSE)
                                  GET /instances/{id}/logs   --> EventSource
                                       |                         |
                                  k8s pods/<pod>/log         Alpine + uPlot render
```

Pod truth stays in k8s; the UI reads only through the controller (never k8s directly).
All controller state added here is **in-memory** (lost on controller restart — acceptable for a
dashboard; `ponytail:` noted).

## Component 1 — `mc-metrics` plugin (new, baked into mc-base)

- New Paper plugin module `plugins/mc-metrics`, built like `stub-game`/`parkour-game`.
- Jar COPYd into `images/mc-base/Dockerfile` → **every** `FROM mc/mc-base:dev` backend inherits it
  for free, no per-game change. (This is the shared mc-base plugin Slice 2 deferred finally paying
  off; v1 only adds metrics, it does NOT consolidate the per-game autosave/confine behavior —
  that refactor is out of scope.)
- Serves `GET /metrics` on internal port **9100** using the JDK's built-in
  `com.sun.net.httpserver.HttpServer` (no Spark/Netty dependency). Cluster-internal, pod-IP only.
  **No auth** — read-only, no secrets. (Auth matters for the deferred `POST /command`, not here.)
- Response:
  ```json
  { "tps": 19.98, "mspt": 3.1, "players": 2, "maxPlayers": 20,
    "uptimeSec": 412, "jvmStartupSec": 6.2 }
  ```
  - `tps` = `Bukkit.getTPS()[0]`, `mspt` = `Bukkit.getAverageTickTime()`
  - players = online / max
  - `uptimeSec` = since plugin enable
  - `jvmStartupSec` = `ManagementFactory.getRuntimeMXBean().getStartTime()` → enable delta
- World-load-time is folded into `jvmStartupSec` rather than parsing ASP "World … loaded in Nms"
  log lines. `ponytail:` comment names the upgrade path (scrape the log line) if a precise
  world-load metric is ever needed.

## Component 2 — controller scrape + read model (in-memory)

- **Scrape ticker** (~2s): for each Ready pod (has `podIP`), `GET http://<podIP>:9100/metrics` with
  a short timeout. Store latest per instance; unreachable/timeout → metrics `null` + stale flag.
- **Startup timing:** record `createdAt` (pod `creationTimestamp`) and `readyAt` (first time the
  controller observes the pod Ready, in the existing pool loop). `startupSeconds = readyAt - createdAt`.
- **`lifecycle(pod) → {state, reason}`** — one pure, unit-tested function (sibling of
  `needed`/`pickAllocatable`). Maps:
  - `metadata.deletionTimestamp` set → **stopping** (reason: "Terminating")
  - `restartCount > 0` or `containerStatuses[].state.waiting.reason == CrashLoopBackOff` →
    **crashing** (reason from `waiting.reason`/`.message` or last `terminated.reason`+`exitCode`)
  - `Ready` condition true → **running**
  - else → **booting** (reason from `waiting.reason`/`.message`, e.g. ContainerCreating/Pulling)
- **Alloc failures:** in-memory per-game counter, incremented when `/allocate` returns 503.

## Component 3 — controller endpoints

- `GET /snapshot` → one-shot JSON, the same object pushed over SSE (for `curl`/tests). Shape:
  ```json
  {
    "games": [
      { "name": "parkour", "poolSize": 2, "booting": 0, "ready": 1, "allocated": 1,
        "total": 2, "registered": 2, "allocFailures": 0 }
    ],
    "instances": [
      { "id": "...", "game": "parkour", "address": "10.244.0.7:25565", "alloc": true,
        "lifecycle": { "state": "running", "reason": "" }, "startupSeconds": 7.4,
        "metrics": { "tps": 19.98, "mspt": 3.1, "players": 1, "maxPlayers": 20,
                     "uptimeSec": 412, "jvmStartupSec": 6.2 }, "stale": false }
    ]
  }
  ```
- `GET /stream` → SSE (`text/event-stream`). On connect, send the current snapshot immediately,
  then push a fresh snapshot on each scrape tick. Plain `net/http` handler using `http.Flusher`;
  no new dependency. A single snapshot-builder function feeds both `/snapshot` and `/stream`.
  - `ponytail:` push cadence = scrape cadence (~2s). True event-driven push (on a k8s pod-watch)
    is a documented upgrade, not v1.
- `GET /instances/{id}/logs?tail=N` → proxies k8s
  `GET /api/v1/namespaces/mc/pods/<pod>/log?tailLines=N` via the SA token the controller already
  has; returns plain text. Normal request/response (not streamed).
- `GET /ui/` → the embedded dashboard page (see Component 4).
- Existing endpoints unchanged: `POST /allocate`, `POST /instances/{id}/done`, `GET /healthz`.

### RBAC

Add `pods/log` (verb `get`) to the controller Role (the controller already lists/creates/deletes
pods). Without it the logs proxy 403s.

## Component 4 — UI (single embedded page)

- One `index.html`, `//go:embed`'d into the controller binary and served at `GET /ui/` — no extra
  Docker COPY, no node toolchain, no build step.
- **Framework via CDN** (user approved a framework, ponytail keeps it build-free): **Alpine.js**
  for reactive state + **uPlot** for the TPS sparkline, both from a CDN `<script>`.
- `new EventSource('/stream')` → on each message, replace Alpine state → re-render. No `setInterval`.
- Views:
  - Cluster cards: total instances, ready/allocated/booting, aggregate players, cluster TPS
    (computed client-side from `instances`).
  - Per-game pool bars: booting / ready / allocated vs `poolSize`, registered count, allocFailures.
  - Per-instance table: state+reason badge, startup time, TPS / MSPT / players, stale indicator;
    row click → log panel (`/instances/{id}/logs`) + a per-instance TPS sparkline (client-side ring
    buffer of recent pushes — no server history).
- The **frontend-design** skill is applied during implementation so it doesn't read as a templated
  default.

## Testing

- `lifecycle(pod)` pure-function unit tests — the one non-trivial branch (each state + reason),
  alongside `needed`/`pickAllocatable` tests.
- `mc-metrics` plugin self-check on the Bukkit-free bits (JSON shape / number formatting), no
  paper-api needed.
- Snapshot-builder covered via `GET /snapshot` in the existing server tests.
- Manual acceptance (kind): `make up` → open `/ui/` → pools fill (booting→ready), live TPS, allocate
  a game → instance flips to allocated, players climbs → kill a pod → state flips to
  crashing/stopping **with the reason** → logs panel shows tail output.

## Explicitly out of scope (deferred, each its own future slice)

- `POST /command` into backends (read-WRITE console RCE — needs auth + allowlist + audit).
- Prometheus / Grafana.
- Live (follow) log streaming — tail-N snapshot only.
- Server-side metric history / time-series store.
- Consolidating per-game autosave/confine behavior into the shared mc-base plugin.
