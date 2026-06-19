# Slice 3 handoff (read this first in a fresh session)

Slice 2 merged. The platform now runs **multiple games from config** and ships two real
games (stub + procedural parkour) following a written image convention. Slice 3 = the
**WebUI: a detailed metrics / observability dashboard** over the platform — so issues are
easy to diagnose and the live state is always visible.

For Slice 3: invoke `/ponytail`, then the brainstorming skill, and feed it this file.

## Slice 3 requirement (from the user — this overrides the old "thin read-only dashboard" framing)

The WebUI must show **truly detailed metrics**, not just a pod list. Explicitly asked for:
startup times, counts, and **TPS** — and "so we can easily fix issues in the future and know
the state at all times." So treat this as an **observability** slice, design around metrics:

- **Counts / pool health:** per game — booting / ready / allocated / total, vs `poolSize`;
  registered-with-Velocity count; allocate failures (503s).
- **Startup times:** per pod, create → Ready latency (the controller already creates pods and
  watches readiness, so it can timestamp this); also game-world load time (ASP logs
  "World … loaded in Nms" / Paper "Done preparing level").
- **TPS / health per running instance:** each Paper backend's TPS, MSPT, player count, uptime.
  This is the part that **invents something new**: the metric has to come *from the backends*.
  Options to weigh during brainstorming — a tiny metrics endpoint baked into mc-base (a shared
  base plugin exposing `/metrics` JSON: TPS via `Bukkit.getTPS()`, MSPT, players, uptime, world
  load time), scraped by the controller and aggregated; vs. per-pod Prometheus + Grafana (heavier,
  probably over-engineered for this — ponytail will push back). Recommended lazy path: a shared
  mc-base metrics plugin + controller aggregates + one static page. Decide in brainstorming.
- This likely means a **shared mc-base plugin finally pays for itself** (the cross-cutting thing
  Slice 2 deferred): it can host both the `/metrics` endpoint AND the stateless autosave-off /
  player-confine behavior currently duplicated per game.

Note: this is bigger than "invents nothing new." The controller needs new read endpoints
(`GET /instances`, `GET /games`, `GET /metrics`) and the backends need to report their own
health. Brainstorm scope carefully; it may split into "metrics pipeline" + "UI" sub-slices.

## What Slice 2 delivered (verified live on `kind`)

- **Config-driven multi-game controller.** Games come from the `minigames` ConfigMap
  (JSON: `[{name,image,poolSize}]`) mounted at `/config/games.json`; the controller runs the
  warm-pool + Velocity-registration loop per game. `GAME`/`POOL_SIZE`/`MINIGAME_IMAGE` env are gone.
- **Parkour** (`mc/minigame-parkour:dev`): a procedurally-generated **void world** (no baked
  `.slime`) — `mc.parkour.Course.generate(seed,length)` builds the course, the plugin places it,
  reaching the finish auto-POSTs `/done`. Seed = `INSTANCE_ID` hash (stable per pod).
- **Convention doc**: `docs/minigame-convention.md` — the contract + stub/parkour as references.
- Verified: both pools spawn + register; `/allocate {game:parkour}` returns the parkour pod;
  unknown game → 404. (In-game compass→parkour→finish→recycle is the human acceptance test.)

## Carryover facts (don't re-derive)

- Controller REST API (`:8080`): `POST /allocate {game}` → `{server,address}` (400 missing game,
  404 unknown game, 503 none ready); `POST /instances/{id}/done`; `GET /healthz`. There is **no
  list/status endpoint yet** — Slice 3 will likely need one (e.g. `GET /instances` or `/games`).
  The controller already lists pods via `k8s.listPods("")` (all minigames); a read handler is small.
- Controller env: `VELOCITY_REGISTER_URL`, `CONTROLLER_URL`, `GAMES_CONFIG` (default `/config/games.json`).
- Pod truth lives in k8s (labels `app=minigame, game=<g>, alloc=<bool>`), not in controller memory
  beyond the `registered` map. A dashboard should read through the controller, not k8s directly.
- Add a game = new image (per `docs/minigame-convention.md`) + `minigames` ConfigMap entry +
  lobby `config.yml` compass entry. `name`/`target`/`game` label must all match.
- `make up` brings up the whole stack; `make build-parkour` / `build-minigame-stub` build games.

## Statelessness (established in Slice 2 — keep it)

- Minigame **game worlds must be in-memory slime worlds**, never anvil/`WorldCreator`. Baked
  `.slime` (stub) or ASP `createEmptyWorld(name,false,props,null)` + `loadWorld` (parkour, null
  loader = temporary). Verified: the parkour pod has **no** `/server/parkour` dir.
- mc-base makes every backend stateless: the default overworld (the one world Paper forces
  through the anvil loader — ASP cannot replace it) is an empty **void** (`level-type=flat`,
  void biome), nether disabled, world autosave off. It still serializes prepared air chunks to
  the pod's **ephemeral** disk — that's scratch, not state; nothing persists across pods (no PVC).
- Confine players: invulnerable on join + `PlayerRespawnEvent` → game spawn, so a death never
  drops them into the void overworld. See `ParkourPlugin`.

## Gotchas hit in Slice 2 (don't repeat)

- Backends must boot `online-mode=false` for Velocity modern forwarding (mc-base entrypoint
  handles it). If you see "Backend server is online-mode!" / "Unable to connect you to lobby",
  that's the cause.
- ASP API to compile against the in-memory world API: repo
  `https://repo.infernalsuite.com/repository/maven-snapshots/`, `compileOnly
  com.infernalsuite.asp:api:4.1.0-SNAPSHOT` (provided at runtime by the ASP server).
- Keep game logic in Bukkit-free classes (`Course`, `Done`) so it unit-tests without paper-api.

## Open Slice 3 questions to settle first (in brainstorming)

1. **Metric source for TPS/health** — backends must self-report. Lazy path: a shared mc-base
   plugin exposing `/metrics` JSON (TPS/MSPT/players/uptime/world-load-time), controller scrapes
   + aggregates. Confirm vs. Prometheus/Grafana (likely over-engineered here).
2. **Read model on the controller** — `GET /instances`, `/games`, `/metrics` JSON. The controller
   already lists pods via `k8s.listPods("")`; startup time = timestamp create→Ready (it watches
   readiness). Keep pod truth in k8s, read through the controller, not k8s directly from the UI.
3. **UI delivery** — recommended start: one static HTML/JS page the controller serves and that
   polls the JSON endpoints (no build step, no framework). Decide auto-refresh interval.
4. **Scope split?** — "metrics pipeline" (backend `/metrics` + controller aggregation) vs. "UI"
   may warrant two sub-slices; brainstorming should check whether one plan stays focused.
