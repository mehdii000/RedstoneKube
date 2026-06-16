# Slice 0 — Skeleton

Dynamic Minecraft server manager for disposable minigames. This is the first
vertical slice: get a player connecting through a custom Velocity proxy into a
custom lobby, all on a local `kind` cluster with real LoadBalancer semantics.
No controller, no dynamic registration, no minigame pods yet — those are Slice 1.

## Goal of this slice

`make up`, point a Minecraft client at the LoadBalancer IP:25565, spawn in the
lobby (invulnerable, flying), click a compass to open a static minigame GUI.
This proves the riskiest base infrastructure works: custom images, slime worlds,
TCP ingress, and Velocity modern forwarding.

## Whole-project context (for orientation only — not built here)

The full system is ~5 subsystems, built as vertical slices:

- **Slice 0 (this doc)** — kind + LoadBalancer → Velocity → lobby, static config.
- **Slice 1** — Go controller talks to k8s API, spawns minigame pods, registers
  them with all Velocity replicas, exposes a REST API; `/play X` works.
- **Slice 2** — base-image convention so a new minigame is added in minutes.
- **Slice 3** — WebUI dashboard over the controller's REST API.

Each slice gets its own spec → plan → implementation cycle.

## Architecture (Slice 0)

```
Minecraft client
      │  TCP 25565
      ▼
Service: velocity (type=LoadBalancer ← cloud-provider-kind assigns an IP)
      ▼
Velocity pod(s)   ── modern forwarding (shared secret) ──┐
      │  static route to "lobby"                          │
      ▼                                                   │
Service: lobby (ClusterIP)                                │
      ▼                                                   │
Lobby pod  (ASP + lobby plugin + baked lobby.slime) ◄─────┘
```

One Velocity replica for now; nothing in the design hardcodes "1" (Slice 1 adds
multiple replicas + controller-pushed registration).

## Key decisions (locked)

- **Base server software:** Advanced Slime Paper (ASP) fork — slime worlds native,
  no plugin load-order games. Exact fork/MC version pinned at build time.
- **World storage:** SWM **file** data source. `.slime` worlds baked into the image.
  The image is the unit of deployment; the world travels with it; zero database.
- **LoadBalancer:** `cloud-provider-kind` — real `LoadBalancer` Services get an IP
  with zero config, so manifests match a real cluster. (MetalLB is a later swap
  with no Service changes.)
- **Protocol:** Minecraft Java = TCP/25565 only. UDP/Geyser deferred.
- **Images:** all custom-built; no itzg. Built locally and `kind load`ed (no registry).
- **Plugin build:** Java + Gradle.
- **Repo location:** `~/Coding/mc-platform`.

## Monorepo layout

```
mc-platform/
  images/
    velocity/    Dockerfile + velocity.toml (modern forwarding, static lobby entry)
    mc-base/     Dockerfile: temurin JRE + ASP jar + entrypoint + paper/SWM config
    lobby/       Dockerfile FROM mc-base + lobby-plugin.jar + lobby.slime
  plugins/
    lobby-plugin/  Gradle Paper plugin (Java)
  deploy/
    kind/        cluster config
    k8s/         velocity (Deploy + LB Svc), lobby (Deploy + Svc), forwarding Secret
  Makefile       up / build / load / apply / smoke / down
  README.md
  docs/superpowers/specs/   this spec
```

Build flow is registry-free: `docker build` → `kind load docker-image` →
`kubectl apply`. `make up` runs the whole sequence (incl. starting
`cloud-provider-kind`).

## Components

### `mc-base` image

- temurin JRE + ASP server jar.
- Entrypoint configures, from env/mounted files:
  - Velocity modern forwarding ON, secret from a mounted file (NOT baked).
  - SWM file data source rooted at `/worlds`.
  - Aikar JVM flags.
- Stateless: slime worlds load into memory; nothing persists back to disk.
- Server working dir is an `emptyDir` so the pod writes nothing durable. Leaves
  room to enable `readOnlyRootFilesystem` later (Slice 1 hardening).

### `lobby` image

- `FROM mc-base`. Adds `lobby-plugin.jar` and a baked `lobby.slime`, sets it as
  the default world loaded on startup.

### `velocity` image

- temurin JRE + Velocity jar + `velocity.toml`: modern forwarding ON, one static
  `lobby` server → `lobby.<namespace>.svc.cluster.local:25565`. Runs non-root.

### `lobby-plugin` (Paper, Java)

- On player join: `setInvulnerable(true)`, allow + enable flight, give a named
  **compass** in a locked hotbar slot.
- Right-click compass → chest-inventory GUI listing minigames from the plugin's
  `config.yml` (static in Slice 0).
- Clicking a minigame entry is a **stub** ("coming soon"). Real
  "connect player to server X" via Velocity plugin-messaging is wired in Slice 1
  when servers exist.
- Raw Bukkit inventory API — no GUI framework for a single menu.

### Networking & secrets

- Service `velocity`, `type=LoadBalancer`, TCP 25565 → Velocity pods. IP assigned
  by `cloud-provider-kind`, reachable from the host.
- Service `lobby`, ClusterIP, TCP 25565 → lobby pods.
- Forwarding secret in a k8s `Secret`, mounted into both `velocity` and `lobby`.
  This is a trust boundary — real secret, never hardcoded in an image.

## Error handling

- Entrypoints fail fast (exit non-zero) if the forwarding secret is missing — a
  misconfigured secret must not silently start an insecure proxy/backend.
- Velocity health: liveness via the proxy answering a server-list ping.
- Lobby pod readiness gates the `lobby` Service so Velocity doesn't route to a
  half-booted server.

## Testing

One runnable check (`make smoke`): a Minecraft **server-list ping** against the
LoadBalancer IP that asserts the proxy answers and reports online. This is the
smallest thing that fails if any link in the chain (LB → Velocity → forwarding →
lobby) breaks. Manual confirmation: connect a real client, verify invuln + flight
+ compass GUI.

## Explicitly out of scope (deferred, marked in code with `ponytail:` comments)

- Controller, dynamic registration, real minigame pods → **Slice 1**
- Multi-replica Velocity registry sync → **Slice 1**
- Central world store (Mongo/S3) → add when worlds need editing without a rebuild
- UDP / Geyser / Bedrock → add when Bedrock support is wanted
- `readOnlyRootFilesystem` hardening → Slice 1 minigame pods
- GUI framework for the compass menu → add when menus multiply
