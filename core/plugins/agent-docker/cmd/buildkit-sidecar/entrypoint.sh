#!/bin/sh
set -e

# Route traffic through gateway (for image pulls).
. /shared/certs/gateway-route.sh

exec buildkitd \
  --addr tcp://0.0.0.0:8372 \
  --root /var/lib/buildkit
