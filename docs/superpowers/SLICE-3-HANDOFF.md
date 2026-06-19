# Slice 3 handoff (read this first in a fresh session)

Slice 2 merged. The platform now runs **multiple games from config** and ships two real
games (stub + procedural parkour) following a written image convention. Slice 3 = the
**WebUI**: a read-only dashboard over the controller's existing API. Invents nothing new.

For Slice 3: invoke `/ponytail`, then the brainstorming skill, and feed it this file.

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

## Gotchas hit in Slice 2 (don't repeat)

- Backends must boot `online-mode=false` for Velocity modern forwarding (mc-base entrypoint
  handles it). If you see "Backend server is online-mode!" / "Unable to connect you to lobby",
  that's the cause.
- Procedural worlds: an empty `ChunkGenerator(){}` via `WorldCreator` = void world, no files.
  Paper still generates a default `world` per server (unused) — harmless, slight boot cost.
- Keep game logic in Bukkit-free classes (`Course`, `Done`) so it unit-tests without paper-api.

## Open Slice 3 question to settle first

- The dashboard needs a **read model**. Add a `GET /instances` (and maybe `/games`) JSON endpoint
  to the controller, then a static read-only WebUI that polls it? Where does the UI run — a tiny
  static page served by the controller, or its own pod/Service? Recommended start: one read
  endpoint on the controller + a single static HTML/JS page it serves (no build step, no framework).
