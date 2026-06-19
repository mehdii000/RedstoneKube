# Minigame image convention

A minigame is a disposable Paper backend the controller spawns, registers with
Velocity, and deletes when the match ends. To add one:

## 1. Image
`FROM mc/mc-base:dev` and bake your plugin jar into `/server/plugins/`. mc-base
already provides ASP, the forwarding secret mount, `online-mode=false`, and SWM.

## 2. World
Provide a world the plugin teleports joiners into — either:
- **baked** a `.slime` world + `worlds.yml` under
  `/opt/config/plugins/SlimeWorldManager/` (see `images/minigame-stub`), or
- **generated** at runtime via `WorldCreator` (see `images/minigame-parkour`,
  which uses an empty `ChunkGenerator` for a void world).

## 3. Game-over contract
When the match ends, POST `{CONTROLLER_URL}/instances/{INSTANCE_ID}/done`. Both
env vars are injected by the controller. The controller then unregisters and
deletes the pod, and reconcile spawns a replacement. Trigger it however the game
ends — an op command (stub's `/endgame`) or a win condition (parkour's finish).

## 4. Testable core
Keep game logic (course generation, win checks, URL building) in **Bukkit-free**
classes so it unit-tests without paper-api on the test classpath — see
`mc.parkour.Course` / `mc.parkour.Done` / `mc.stub.Done`.

## 5. Register the game (two places)
- `deploy/k8s/controller.yaml` → the `minigames` ConfigMap: `{name, image, poolSize}`.
- `plugins/lobby-plugin/src/main/resources/config.yml` → a compass entry with
  `target: <name>`.

The `name` in the ConfigMap and the compass `target` must match the `game` label
the controller sets on pods.
