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

# Run buildkitd as unprivileged user (rootless mode).
# --oci-worker-no-process-sandbox: skip cgroup/PID isolation for build processes.
exec su-exec buildkit buildkitd \
  --addr tcp://0.0.0.0:8372 \
  --root /home/buildkit/.local/share/buildkit \
  --oci-worker-no-process-sandbox
