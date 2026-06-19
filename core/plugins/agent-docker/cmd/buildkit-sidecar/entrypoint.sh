#!/bin/sh
set -e

# Route traffic through gateway (for image pulls).
. /usr/local/bin/gateway-route.sh

# Run buildkitd as root. This is safe because:
# - This container has NO docker.sock (cannot escape to Docker API)
# - This container has NO secrets (nothing to exfiltrate)
# - Container boundary provides isolation from host
exec buildkitd \
  --addr tcp://0.0.0.0:8372 \
  --root /var/lib/buildkit
