#!/bin/sh
set -e

# Wait for gateway to write the routing script (with timeout).
ROUTE_SCRIPT="/shared/certs/gateway-route.sh"
TIMEOUT=30
ELAPSED=0
while [ ! -f "$ROUTE_SCRIPT" ] && [ "$ELAPSED" -lt "$TIMEOUT" ]; do
    sleep 1
    ELAPSED=$((ELAPSED + 1))
done

if [ -f "$ROUTE_SCRIPT" ]; then
    . "$ROUTE_SCRIPT" || echo "[buildkit-entrypoint] WARNING: gateway routing setup failed" >&2
else
    echo "[buildkit-entrypoint] WARNING: $ROUTE_SCRIPT not found after ${TIMEOUT}s — no outbound network" >&2
fi

# Switch to rootless user and start buildkitd via rootlesskit.
exec su-exec user rootlesskit buildkitd \
  --addr tcp://0.0.0.0:8372 \
  --oci-worker-no-process-sandbox
