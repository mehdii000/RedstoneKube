#!/bin/sh
set -e
# Trust boundary: refuse to start an insecure proxy if the forwarding secret is absent.
[ -s /secret/forwarding.secret ] || { echo "FATAL: /secret/forwarding.secret missing/empty" >&2; exit 1; }
exec java -Xms256M -Xmx512M -jar /app/velocity.jar
