#!/bin/sh
set -e

# Route all traffic through gateway (iptables DNAT, DNS, CA cert).
. /shared/certs/gateway-route.sh

# Run docker-proxy in foreground.
exec docker-proxy
