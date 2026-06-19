# Minigame image convention

A minigame is a disposable Paper backend the controller spawns, registers with
Velocity, and deletes when the match ends. To add one:

## 1. Image
`FROM mc/mc-base:dev` and bake your plugin jar into `/server/plugins/`. mc-base
already provides ASP, the forwarding secret mount, `online-mode=false`, and SWM.

## 2. World — must be stateless (no anvil)
Minigames are disposable; nothing should persist. Use an **in-memory slime world**, never a
vanilla anvil world. Two ways:
- **baked** a `.slime` world + `worlds.yml` under
  `/opt/config/plugins/SlimeWorldManager/` (see `images/minigame-stub`), or
- **generated in memory** at runtime via ASP's API — `createEmptyWorld(name, false, props, null)`
  (a `null` loader = temporary, never saved) then `loadWorld(world, true)`, and place blocks into
  it (see `images/minigame-parkour`).

mc-base already makes every backend stateless: the one world Paper forces through the anvil
loader (the default overworld — ASP cannot replace it) is configured as an empty void with the
nether disabled, and world autosave is off. Do **not** use `WorldCreator`/anvil worlds.

## 2b. Keep players out of the void overworld
Because the default overworld is empty void, a death would drop a player into nothing. Confine
players to your game world: make them invulnerable on join and set the respawn location to your
spawn (`PlayerRespawnEvent`). See `ParkourPlugin`.

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
