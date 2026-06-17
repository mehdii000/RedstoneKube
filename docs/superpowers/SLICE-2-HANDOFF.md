# Slice 2 handoff (read this first in a fresh session)

Slice 1 merged. The controller + dynamic registration loop works end-to-end with one
placeholder game (`mc/minigame-stub:dev`). Slice 2 = the **minigame image convention**.

For Slice 2: invoke `/ponytail`, then the brainstorming skill, and feed it this file.

## What Slice 1 delivered (verified live on `kind`)

- Go **controller** (`controller/`, stdlib only, no client-go): warm pool of bare minigame
  Pods, REST API on :8080. Verified: pool spawn → register with Velocity → `/allocate`
  (marks `alloc=true` + refill) → `/done` (unregister + delete, pool maintained).
- **velocity-register** plugin (baked into `mc/velocity:dev`): live `registerServer` /
  `unregisterServer` over HTTP on :8080, bearer `CONTROLLER_TOKEN`.
- **lobby** compass click → controller `/allocate` → BungeeCord `Connect` (Velocity native).
- `make up` brings up everything; `mc/minigame-stub:dev` is the one placeholder game.

## Carryover facts (don't re-derive)

- Controller env knobs: `GAME`, `POOL_SIZE`, `MINIGAME_IMAGE`, `VELOCITY_REGISTER_URL`,
  `CONTROLLER_URL`. Today it manages ONE game type; Slice 2 needs multiple (per-game image
  + pool size). The pool/registration logic already keys on the `game` label.
- A minigame pod is a bare Pod (labels `app=minigame, game=<g>, alloc=<bool>`), mounts the
  forwarding secret at `/secret`, env `INSTANCE_ID`+`CONTROLLER_URL`, TCP readiness on 25565.
  Pod manifest is a JSON template in `controller/k8s.go:podManifest`.
- Game-over contract: the pod POSTs `{CONTROLLER_URL}/instances/{INSTANCE_ID}/done`; the
  stub triggers it from an op `/endgame` command (`plugins/stub-game`). Real games call it
  when their match ends.
- velocity-register: `POST /servers {name,address}` / `DELETE /servers/{name}` on :8080,
  bearer `CONTROLLER_TOKEN`. Idempotent upsert (guarded unregister-then-register).
- Do NOT build a scaffolding/templating generator until 2–3 games hurt by hand (roadmap).

## Gotchas hit in Slice 1 (don't repeat)

- Velocity `unregisterServer` THROWS if the server name is absent — never call it
  unguarded; check `proxy.getServer(name)` first. (Cost us an EOF debugging round.)
- `com.sun.net.httpserver` silently closes the connection on a handler throwable (client
  sees EOF, nothing in logs) — always wrap handlers in try/catch and return a status.
- A pure helper on a `JavaPlugin`-extending class can't be unit-tested without paper-api on
  the test classpath — keep testable logic in a Bukkit-free class (see `Menu`, `Done`,
  `Allocate`).

## Open Slice 2 question to settle first

- Multi-game config: per-game image + pool size. Where does it live — controller env list,
  a ConfigMap, or a CRD? Recommended start: a small ConfigMap of `{game: {image, poolSize}}`.
