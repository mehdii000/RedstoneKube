#!/bin/sh
# Generate worlds/lobby.slime: boot a throwaway mc-base, generate a superflat
# "lobby" world, then convert it to slime format with the ASP importer.
# Requires: docker, curl, and a host java (21+) for the importer.
set -e
cd "$(dirname "$0")/../.."          # repo root
. images/versions.env               # ASP_IMPORTER_URL

IMPORTER=$(mktemp --suffix=.jar)
WORK=$(mktemp -d)
cleanup() { docker rm -f lobbygen >/dev/null 2>&1 || true; rm -rf "$IMPORTER" "$WORK"; }
trap cleanup EXIT

echo ">> downloading importer"
curl -fsSL "$ASP_IMPORTER_URL" -o "$IMPORTER"

echo ">> generating superflat 'lobby' world"
docker rm -f lobbygen >/dev/null 2>&1 || true
docker run -d --name lobbygen --entrypoint sh mc/mc-base:dev -c '
  cd /server
  printf "level-name=lobby\nlevel-type=minecraft:flat\ngenerate-structures=false\nspawn-npcs=false\nspawn-animals=false\nspawn-monsters=false\n" > server.properties
  exec java -Xms1G -Xmx1G -jar /opt/server.jar nogui' >/dev/null

for i in $(seq 1 180); do
  docker logs lobbygen 2>&1 | grep -qE "Done \(" && break
  sleep 1
done
docker logs lobbygen 2>&1 | grep -qE "Done \(" || { echo "FATAL: world never finished generating" >&2; exit 1; }

echo ">> saving + extracting world"
docker stop lobbygen >/dev/null     # SIGTERM -> Paper saves the world
docker cp lobbygen:/server/lobby "$WORK/lobby"

echo ">> converting to slime"
java -jar "$IMPORTER" "$WORK/lobby" --accept
mkdir -p worlds
cp "$WORK/lobby.slime" worlds/lobby.slime
echo ">> wrote worlds/lobby.slime ($(wc -c < worlds/lobby.slime) bytes)"
