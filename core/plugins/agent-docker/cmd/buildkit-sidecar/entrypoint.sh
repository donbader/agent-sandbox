#!/bin/sh
set -e

# Wait for gateway to write the routing script (with timeout).
# The gateway writes /shared/certs/gateway-route.sh after becoming healthy.
# Sidecars start after the agent which depends on gateway:healthy, but
# there can be a brief race on slow CI.
ROUTE_SCRIPT="/shared/certs/gateway-route.sh"
TIMEOUT=30
ELAPSED=0
while [ ! -f "$ROUTE_SCRIPT" ] && [ "$ELAPSED" -lt "$TIMEOUT" ]; do
    sleep 1
    ELAPSED=$((ELAPSED + 1))
done

if [ -f "$ROUTE_SCRIPT" ]; then
    # Source routing config. Failures here should not prevent buildkitd from starting.
    . "$ROUTE_SCRIPT" || echo "[buildkit-entrypoint] WARNING: gateway routing setup failed" >&2
else
    echo "[buildkit-entrypoint] WARNING: $ROUTE_SCRIPT not found after ${TIMEOUT}s — no outbound network" >&2
fi

exec buildkitd \
  --addr tcp://0.0.0.0:8372 \
  --root /var/lib/buildkit \
  --oci-worker-no-process-sandbox
