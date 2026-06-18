# Slice 2 — minigame image convention + multi-game controller (design)

Status: approved 2026-06-18. Built lazy (ponytail): stdlib/native first, shortest diff.

## Goal

Generalize the controller from one hardcoded game to a **config-driven set** of game
types, and prove the generalization by adding a real second game (**parkour**) that
follows a written **image convention**. The lobby compass offers both games; each runs
the same disposable warm-pool loop from Slice 1.

**No generator / scaffolding.** The convention is *documented and demonstrated*, not
codegen'd — build the generator only after 2–3 games make the repetition hurt (roadmap).

## What Slice 1 already gives us (don't re-derive)

- Controller warm-pool + Velocity registration loop, keyed on the `game` label.
- velocity-register upsert (`POST/DELETE /servers`), bearer `CONTROLLER_TOKEN`.
- lobby compass `onClick` already maps slot → entry `target` → `/allocate(target)` →
  BungeeCord `Connect`. It already supports multiple entries.
- Game-over contract: pod POSTs `{CONTROLLER_URL}/instances/{INSTANCE_ID}/done`.
- Backends boot `online-mode=false` (mc-base entrypoint) for Velocity modern forwarding.

## 1. Games config — ConfigMap as JSON

A ConfigMap `minigames` (in `deploy/k8s/controller.yaml`) mounted at `/config/games.json`:

```json
[{"name":"stub","image":"mc/minigame-stub:dev","poolSize":1},
 {"name":"parkour","image":"mc/minigame-parkour:dev","poolSize":1}]
```

The controller reads it once at startup with `encoding/json`. This **replaces** the
single-game `GAME` / `MINIGAME_IMAGE` / `POOL_SIZE` env trio.

`ponytail: JSON not YAML — stdlib parses it, no dependency. Edit the ConfigMap + roll the
controller to change games; add live reload only when that restart actually annoys someone.`

## 2. Controller multi-game refactor (`controller/main.go`)

- `type Game struct{ Name, Image string; PoolSize int }`; `Controller.games []Game`
  replaces the single `game/image/poolSize` fields.
- `createPod` func field becomes `func(name, game, image string) error` (it already maps
  to `k8s.createPod(name, game, image, controllerURL)`).
- `reconcile`: loop over `games`; per game `listPods`+`needed`+`createPod`. Registration
  sync runs over **all** minigame pods.
- `handleAllocate`: require `body.game`; return **404** if it is not a configured game
  (no single default once there are several), **503** if no pod of that game is ready.
- `handleDone`: list **all** minigame pods (`listPods("")`), validate the id is a known
  pod, then unregister + delete; the next reconcile tick refills.
- Pure cores (`needed`, `pickAllocatable`) are unchanged.

`listPods("")` = all minigames: a one-line `k8s.go` change so an empty game drops the
`game=` label filter (keeps only `app=minigame`).

## 3. Parkour game — procedurally generated (the worked example)

Deliberately uses a **runtime-generated void world**, not a baked `.slime`, to (a) be
simpler to build — no world tooling — and (b) show the convention supports either world
strategy.

- `plugins/parkour-game/`:
  - `onEnable`: create a void world `parkour` (`WorldCreator` + an empty `ChunkGenerator`),
    then place the course blocks from the generated coordinates.
  - `onJoin`: teleport the player to the start platform.
  - `onMove`: reaching the finish platform → win → POST `/done`; falling into the void
    (below a Y floor) → teleport back to the start platform.
  - **Pure testable core** (Bukkit-free, mirrors `Done`/`Menu`):
    - `Course.generate(seed, length) -> List<Vec3>` — deterministic given a seed; each step
      a small random horizontal gap (2–4) and ±1 height. Unit-tested for determinism +
      reachable gaps.
    - `atFinish(pos, finish)` and `doneUrl(base, id)` — unit-tested.
  - The Bukkit class only places blocks from `generate(...)` and wires events. ~50–70 lines.
- `images/minigame-parkour/`: `FROM mc/mc-base:dev`, `COPY parkour-plugin.jar`. **No** world
  files, **no** `worlds.yml` — leaner than `minigame-stub`.

## 4. Lobby compass

Add a `parkour` entry to the lobby `config.yml`. `onClick` already routes
slot → `target` → `/allocate`, so there is **no lobby code change** — config only. The
list stays static (dynamic discovery from the controller is YAGNI for two games).

## 5. Convention doc — `docs/minigame-convention.md`

The contract a minigame image must satisfy, with parkour + stub as the two reference impls:

- `FROM mc/mc-base:dev`.
- Bake a plugin jar that: teleports joiners into a world (baked `.slime` **or**
  runtime-generated), and POSTs `{CONTROLLER_URL}/instances/{INSTANCE_ID}/done` when the
  match ends.
- `INSTANCE_ID` + `CONTROLLER_URL` arrive via env (controller sets them).
- TCP readiness on 25565; mounts the forwarding secret at `/secret` (mc-base handles it).
- Register the game in two places: the `minigames` ConfigMap (image + poolSize) and the
  lobby `config.yml` (compass entry).

## 6. Makefile + deploy

- New targets: `build-parkour` (build plugin jar → image). No parkour-world target.
- Wire `build-parkour` into `load` (build + `kind load mc/minigame-parkour:dev`) and keep
  `apply` applying `controller.yaml` (now with the ConfigMap).
- `controller.yaml`: add the `minigames` ConfigMap + volume mount at `/config`; drop the
  `GAME`/`MINIGAME_IMAGE`/`POOL_SIZE` env.

## Runnable checks

- Go unit tests: two-game `reconcile` (per-game pool via `needed`), unknown-game
  `/allocate` → 404, `games.json` parse, `listPods("")` selector.
- Parkour plugin: `Course.generate` determinism + gap-reachability, `atFinish`, `doneUrl`.
- End-to-end (`make up`): compass shows 2 games → click parkour → reach finish → auto
  `/done` → pod recycles; stub still works.

## Deliberately deferred (NOT Slice 2)

- Scaffolding/templating generator — only after the convention is proven by hand here.
- Dynamic lobby game discovery from the controller — static `config.yml` is fine for two.
- Live config reload — restart the controller to change games.
- Richer lifecycle (idle reclaim, per-game timeouts) — game-end is explicit `/done` only.
