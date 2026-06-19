#!/bin/sh
set -e

# Route all traffic through gateway (iptables DNAT, DNS, CA cert).
. /usr/local/bin/gateway-route.sh

# Run docker-proxy in foreground.
exec docker-proxy
