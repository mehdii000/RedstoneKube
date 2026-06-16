FROM mc/mc-base:dev
# Plugin jar + baked slime world + SWM world entry (overlaid onto /server by the entrypoint).
# ponytail: rely on the plugin's bundled default config.yml (saveDefaultConfig) — no separate copy.
COPY lobby-plugin.jar /server/plugins/lobby-plugin.jar
COPY lobby.slime /worlds/lobby.slime
COPY worlds.yml /opt/config/plugins/SlimeWorldManager/worlds.yml
