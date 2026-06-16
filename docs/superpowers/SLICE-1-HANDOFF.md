# Slice 1 handoff (read this first in a fresh session)

Slice 0 is merged to `main`. Start Slice 0's spec/plan are in `docs/superpowers/`.
For Slice 1: invoke `/ponytail` then the brainstorming skill, and feed it this file.

## Slice 1 goal (from the project decomposition)

Go **controller** that talks to the k8s API to spawn/kill minigame pods, registers
them with Velocity, and exposes a REST API. End state: clicking a minigame in the
lobby compass GUI spawns/joins a real minigame server.

## Carryover facts (don't re-derive these)

- **ASP build API** (Infernal Suite): `https://api.infernalsuite.com/v1/projects/asp/{buildId}/download/{fileId}`.
  Pinned in `images/versions.env` (MC 1.21.8, buildId `2cd8f78b-...`). Three jars per build:
  `asp-server.jar`, `asp-plugin-4.1.0.jar` (SWM mgmt plugin), `importer-4.1.0.jar` (anvil→slime).
- **SWM config schema** (real, from the jar):
  - `plugins/SlimeWorldManager/sources.yml` → `file: { path: /worlds }` (baked in `mc-base`).
  - `plugins/SlimeWorldManager/worlds.yml` → per-world: `source/difficulty/spawn/allowMonsters/`
    `allowAnimals/loadOnStartup/readOnly/saveBlockTicks/saveFluidTicks/savePoi`. See `images/lobby/worlds.yml`.
  - No "slime world as main world" config exists → we teleport players into the slime world on join.
- **Velocity** has no native REST to add servers. Modern forwarding is on; secret is a k8s Secret
  `velocity-forwarding` mounted at `/secret/forwarding.secret`. Dynamic registration options for Slice 1:
  (a) a custom Velocity plugin that polls the controller / watches k8s, or (b) the controller writes
  velocity.toml + reload. Decide in brainstorming. Multi-replica registry sync is the real hard part.
- **Player→server move**: Velocity plugin-messaging `Connect` (BungeeCord channel). The lobby plugin's
  compass click is currently a stub (`LobbyPlugin.onClick`) — wire it here.
- **Local loop**: `make up` / `make smoke` / `make down`. Images built locally + `kind load`ed, no registry.
  Build images with buildkit (manifest list is fine, `kind load docker-image` works).
- **Gotchas already hit**: base image has UID 1000 taken (use `useradd -r mc`, no fixed UID); `ADD <url>`
  jars land root:0600 → must `chmod 0644`; Velocity needs an empty `[forced-hosts]` or it injects bad defaults;
  Gradle 9 needs `testRuntimeOnly junit-platform-launcher`.

## Cluster is currently UP

`kind` cluster `mc` + cloud-provider-kind are running with velocity+lobby deployed (LB IP 172.18.0.3).
`make down` to reclaim resources, or leave it for Slice 1 dev.
