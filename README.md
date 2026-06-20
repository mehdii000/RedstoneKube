# mc-platform

Dynamic Minecraft server manager for disposable minigames, on Kubernetes (local `kind`).
Custom images only (no itzg). A custom Velocity proxy fronts a custom Advanced Slime Paper
lobby; a Go controller keeps a warm pool of minigame pods, wires them into Velocity, and
recycles them — with a live metrics dashboard over the whole platform.

**Status:** Slices 0–3 done. Lobby + dynamic minigames (`stub`, `parkour`) + controller +
observability dashboard all run end-to-end. See `AGENTS.md` for the slice-by-slice state and
`docs/superpowers/` for specs/plans.

## How it fits together

- **Velocity proxy** (`images/velocity`) — the public entrypoint, gets a LoadBalancer IP. Its
  `velocity-register` plugin lets the controller register/unregister backends live.
- **Lobby** (`images/lobby`) — ASP server with a baked `.slime` world; compass GUI sends players
  to a game by calling the controller's `POST /allocate`.
- **Controller** (`controller/`) — Go, stdlib only, talks to the k8s REST API. Owns a warm pool
  per game (`minigames` ConfigMap), refills/recycles pods, syncs Velocity registration, and
  serves the metrics dashboard. Reaps idle abandoned games (see below).
- **Minigames** (`images/minigame-stub`, `images/minigame-parkour`) — `FROM mc-base`. `parkour`
  is a real procedurally-generated void parkour that auto-`/done`s on win.
- **mc-base** (`images/mc-base`) — shared Paper base; bakes the `mc-metrics` plugin so every
  backend serves `GET :9100/metrics` (TPS/MSPT/players/uptime/jvmStartup).

## Prerequisites

- `docker`, `kind`, `kubectl`, `go`, `openssl`
- `cloud-provider-kind` — `go install sigs.k8s.io/cloud-provider-kind@latest`
  (provides LoadBalancer IPs to `kind`). The Makefile runs it from `$(go env GOPATH)/bin`.
- Paper plugins build inside the `gradle:jdk21` Docker image — no host gradle needed.

Pinned upstream versions live in `images/versions.env` (Velocity build, ASP build, MC version).

## One-time: generate the lobby world

```bash
make lobby-world   # boots a throwaway ASP container, creates worlds/lobby.slime
```

## Run

```bash
make up      # create cluster + build/load all images + apply manifests
make smoke   # status-ping the LoadBalancer IP; prints OK on success
make down    # tear everything down
```

`make up` ends watching the `velocity` Service — wait for `EXTERNAL-IP` to appear,
then Ctrl-C the watch and run `make smoke`.

## Play it

Connect a Minecraft 1.21.8 client to `<velocity EXTERNAL-IP>:25565`. You spawn in the lobby
(invulnerable, flying) with a compass in slot 0 that opens the Minigames menu. Click an entry
and the controller allocates a warm pod and connects you. `parkour` drops you into a generated
course that sends you back to the lobby on completion.

## Metrics dashboard

The controller serves a live SSE dashboard (startup times, pool counts, per-instance
TPS/health/players, lifecycle state, logs). The controller Service is ClusterIP, so port-forward:

```bash
kubectl -n mc port-forward svc/controller 8080:8080
# then open http://localhost:8080/ui/
```

## Idle-game reaping

The controller stops minigame instances players have abandoned: an **allocated** instance that
reports **0 players** for ≥ `IDLE_TIMEOUT` (env, default `5m`) is unregistered and deleted, and
the pool refills. Warm pool instances are never reaped, so joins stay fast.

## Make targets

| Target | Does |
|---|---|
| `cluster` / `down` | create / delete the kind cluster + cloud-provider-kind |
| `build-velocity` `build-velreg` | build the proxy image + its register plugin |
| `build-base` | build mc-base (bakes the `mc-metrics` plugin) |
| `build-lobby` `build-controller` | build the lobby + controller images |
| `build-minigame-stub` `build-parkour` | build the minigame images |
| `plugin` | build the lobby plugin jar (dockerized gradle) |
| `lobby-world` | generate `worlds/lobby.slime` |
| `load` / `apply` | load images into kind / apply manifests + secret |
| `up` / `smoke` | full bring-up / smoke ping |
