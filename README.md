# mc-platform

Dynamic Minecraft server manager for disposable minigames, on Kubernetes.

**Slice 0 (current):** join a custom Velocity proxy → custom Advanced Slime Paper
lobby on a local `kind` cluster with a real LoadBalancer IP. No controller / no
minigame pods yet — see `docs/superpowers/specs/` and `docs/superpowers/plans/`.

## Prerequisites

- `docker`, `kind`, `kubectl`, `go`, `openssl`
- `cloud-provider-kind` — `go install sigs.k8s.io/cloud-provider-kind@latest`
  (provides LoadBalancer IPs to `kind`). The Makefile runs it from `$(go env GOPATH)/bin`.
- The Paper plugin builds inside the `gradle:jdk21` Docker image — no host gradle needed.

Pinned upstream versions live in `images/versions.env` (Velocity build, ASP build, MC version).

## One-time: generate the lobby world

```bash
make lobby-world   # boots a throwaway ASP container, creates worlds/lobby.slime
```

## Run

```bash
make up      # create cluster + build/load images + apply manifests
make smoke   # status-ping the LoadBalancer IP; prints OK on success
make down    # tear everything down
```

`make up` ends watching the `velocity` Service — wait for `EXTERNAL-IP` to appear,
then Ctrl-C the watch and run `make smoke`.

## Manual confirmation

Connect a Minecraft 1.21.8 client to `<velocity EXTERNAL-IP>:25565`. You should
spawn in the lobby (invulnerable, flying) with a compass in slot 0 that opens the
Minigames menu. Clicking an entry shows a Slice-1 stub message.

## Make targets

| Target | Does |
|---|---|
| `cluster` / `down` | create / delete the kind cluster + cloud-provider-kind |
| `build-velocity` / `build-base` / `build-lobby` | build the images |
| `plugin` | build the lobby plugin jar (dockerized gradle) |
| `lobby-world` | generate `worlds/lobby.slime` |
| `load` / `apply` | load images into kind / apply manifests + secret |
| `up` / `smoke` | full bring-up / smoke ping |
