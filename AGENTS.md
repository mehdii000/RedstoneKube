# AGENTS.md

Orientation for AI agents working on this repo. Humans: see `README.md`.

## What this is

Dynamic Minecraft server manager for disposable minigames on Kubernetes (local `kind`).
Custom images only (no itzg). Built as independent vertical slices, each with its own
spec → plan → implementation.

## Current state

- **Slice 0 (Skeleton) — DONE, merged to `main`.** kind + cloud-provider-kind LoadBalancer →
  custom Velocity proxy → custom Advanced Slime Paper lobby with a baked `.slime` world.
  Lobby plugin: invuln + flight + compass GUI (minigame click is a stub).
- **Slice 1 (Controller + dynamic registration) — DONE, merged to `main`.** Go controller
  owns a warm pool of bare minigame Pods (k8s REST, no client-go), self-recycled via
  `POST /done`; velocity-register plugin registers backends live; compass click allocates
  + Connects. Placeholder game: `mc/minigame-stub:dev`.
- **Slice 2 (Minigame image convention) — NEXT, not started.**

## Roadmap

Build in dependency order, one slice per session:

- **Slice 1 — Controller + dynamic registration** (DONE). Go REST API → k8s → spawn/kill
  minigame pods → register with Velocity → wire the compass to send players.
- **Slice 2 — Minigame image convention** (NEXT). `FROM mc-base` + plugin + baked `.slime`, declared
  to the controller. Do NOT build a scaffolding/templating generator until 2-3 games hurt by hand.
- **Slice 3 — WebUI.** Read-only dashboard over the controller's API. Invents nothing new.

After Slice 3 you have a working Hypixel-lite. Everything below was **deliberately deferred** —
build each only when a concrete need shows up, each as its own spec→plan→session. Do not write
detailed plans for them in advance (that's the bloat ponytail exists to prevent):

- Multi-replica Velocity registry sync — when one proxy isn't enough (Slice 1 won't hardcode "1").
- Central world store (Mongo/S3) — when worlds need editing without an image rebuild.
- Global data / persistence (stats, currencies) — first stateful piece; needs its own brainstorm.
- Observability + autoscaling — when game volume demands it.

## Where to look

| You want… | Read |
|---|---|
| Next-session entry point + carryover facts/gotchas | `docs/superpowers/SLICE-2-HANDOFF.md` |
| What was designed and why | `docs/superpowers/specs/` |
| Task-by-task build plans | `docs/superpowers/plans/` |
| How to run it locally | `README.md` |
| Pinned upstream versions | `images/versions.env` |

## Starting the next slice

1. Invoke `/ponytail` (this project is built lazy: stdlib/native first, shortest diff).
2. Invoke the brainstorming skill; feed it `docs/superpowers/SLICE-1-HANDOFF.md`.
3. Brainstorm → writing-plans → execute. One slice per session to keep context lean.
4. Open Slice-1 question to settle first: minigame **lifecycle model** (on-demand vs warm pool).
   Recommended start: on-demand.

## Conventions (established in Slice 0)

- Monorepo: each component is its own dir with a Dockerfile. Images tagged `mc/<name>:dev`.
- Registry-free loop: `docker build` → `kind load docker-image` → `kubectl apply`. `make` drives everything.
- All k8s resources in namespace `mc`.
- Forwarding secret is a k8s Secret, never baked into an image; entrypoints fail-fast if it's missing.
- Minigame/lobby pods are stateless: slime worlds load into memory, nothing persists back.
- Mark deliberate shortcuts with a `ponytail:` comment naming what's skipped and when to add it.
- Non-trivial logic leaves one runnable check (see `tools/smoke`, `plugins/lobby-plugin/.../MenuTest.java`).
