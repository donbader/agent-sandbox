#!/bin/sh
# runc-wrapper: injects gateway CA cert into buildkit RUN containers.
# Mounted as --oci-worker-binary so buildkit calls this instead of runc directly.
# Adds bind mounts for cert bundle + sets env vars for Node.js/OpenSSL trust.

CERT_BUNDLE="/etc/ssl/certs/ca-certificates.crt"
GATEWAY_CA="/shared/certs/ca.crt"

# Find --bundle argument to locate config.json
BUNDLE=""
PREV=""
for arg in "$@"; do
    if [ "$PREV" = "--bundle" ]; then
        BUNDLE="$arg"
        break
    fi
    PREV="$arg"
done

# Inject CA cert mounts + env vars if bundle found and certs exist
if [ -n "$BUNDLE" ] && [ -f "$BUNDLE/config.json" ] && [ -f "$GATEWAY_CA" ]; then
    jq --arg bundle "$CERT_BUNDLE" --arg ca "$GATEWAY_CA" '
        .mounts += [
            {"source": $bundle, "destination": "/etc/ssl/certs/ca-certificates.crt", "type": "bind", "options": ["ro","rbind"]},
            {"source": $ca, "destination": "/etc/ssl/certs/gateway-ca.crt", "type": "bind", "options": ["ro","rbind"]}
        ] |
        .process.env += [
            "NODE_EXTRA_CA_CERTS=/etc/ssl/certs/gateway-ca.crt",
            "SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt"
        ]
    ' "$BUNDLE/config.json" > "$BUNDLE/config.json.tmp" && \
    mv "$BUNDLE/config.json.tmp" "$BUNDLE/config.json"
fi

exec /usr/bin/runc "$@"
