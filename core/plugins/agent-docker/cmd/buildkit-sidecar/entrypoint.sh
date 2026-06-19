#!/bin/sh
set -e

# Route traffic through gateway (for image pulls).
. /usr/local/bin/gateway-route.sh

# Run buildkitd as non-root user.
# No docker.sock access — build steps cannot escape to Docker API.
exec su -s /bin/sh buildkit -c '
  exec buildkitd \
    --addr tcp://0.0.0.0:8372 \
    --oci-worker-no-process-sandbox \
    --root /var/lib/buildkit
'
