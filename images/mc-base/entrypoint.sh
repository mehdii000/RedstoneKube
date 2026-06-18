#!/bin/sh
set -e
# Trust boundary: never boot a backend that would accept unauthenticated proxy traffic.
[ -s /secret/forwarding.secret ] || { echo "FATAL: /secret/forwarding.secret missing/empty" >&2; exit 1; }
SECRET=$(cat /secret/forwarding.secret)

mkdir -p /server/config /worlds
# Overlay any baked config (paper velocity-forwarding + SWM data source/world list) onto /server.
[ -d /opt/config ] && cp -r /opt/config/. /server/ 2>/dev/null || true

# Inject the forwarding secret at runtime (never baked into an image).
if [ -f /server/config/paper-global.yml ]; then
  sed -i "s|__SECRET__|${SECRET}|" /server/config/paper-global.yml
fi

# Behind Velocity modern forwarding the backend must NOT do its own auth, or Velocity
# rejects it with "Backend server is online-mode!". Paper merges missing keys, keeping this.
if [ -f /server/server.properties ] && grep -q '^online-mode=' /server/server.properties; then
  sed -i 's/^online-mode=.*/online-mode=false/' /server/server.properties
else
  echo 'online-mode=false' >> /server/server.properties
fi

cd /server
# Aikar flags: https://docs.papermc.io/paper/aikar-flags
# ponytail: 1-2G heap hardcoded; make it an env knob only when a minigame needs a different size.
exec java -Xms1G -Xmx2G \
  -XX:+UseG1GC -XX:+ParallelRefProcEnabled -XX:MaxGCPauseMillis=200 \
  -XX:+UnlockExperimentalVMOptions -XX:+DisableExplicitGC -XX:+AlwaysPreTouch \
  -XX:G1HeapRegionSize=8M -XX:G1NewSizePercent=30 -XX:G1MaxNewSizePercent=40 \
  -XX:G1ReservePercent=20 -XX:InitiatingHeapOccupancyPercent=15 \
  -jar /opt/server.jar nogui
