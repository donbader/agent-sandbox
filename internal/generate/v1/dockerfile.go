package v1

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
)

// Presets maps @builtin/* to base image + install commands.
var Presets = map[string]struct {
	BaseImage string
	Installs  []string
}{
	"@builtin/codex": {
		BaseImage: "node:24-slim",
		Installs: []string{
			"apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates iptables iputils-ping && rm -rf /var/lib/apt/lists/*",
			"--mount=type=cache,target=/root/.npm npm install -g @openai/codex@0.136.0",
		},
	},
	"@builtin/claude-code": {
		BaseImage: "node:24-slim",
		Installs: []string{
			"apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates iptables iputils-ping && rm -rf /var/lib/apt/lists/*",
			"--mount=type=cache,target=/root/.npm npm install -g @anthropic-ai/claude-code",
		},
	},
	"@builtin/pi": {
		BaseImage: "node:24-slim",
		Installs: []string{
			"apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates iptables iputils-ping && rm -rf /var/lib/apt/lists/*",
			"--mount=type=cache,target=/root/.npm npm install -g @anthropic-ai/claude-code",
		},
	},
}

// entrypointScript is the transparent proxy bootstrap script written to .build/entrypoint.sh.
// It runs inside the agent container on startup and:
//  1. Waits for the gateway health endpoint to be ready.
//  2. Redirects outbound TCP 443 → gateway:8443 (MITM proxy) via iptables DNAT.
//  3. Redirects outbound UDP/TCP 53 → gateway:53 (DNS) via iptables DNAT.
//  4. Installs the gateway CA certificate into the system trust store.
//  5. Execs the original CMD so no PID is wasted.
const entrypointScript = `#!/bin/sh
set -e

# Wait for gateway to be healthy before setting up routing.
echo "[entrypoint] waiting for gateway..."
until curl -sf http://gateway:8080/health >/dev/null 2>&1; do
    sleep 1
done
echo "[entrypoint] gateway ready"

# Resolve gateway IP (getent may not exist in slim images, fall back to ping).
GATEWAY_IP=""
if command -v getent >/dev/null 2>&1; then
    GATEWAY_IP=$(getent hosts gateway | awk '{print $1}')
fi
if [ -z "$GATEWAY_IP" ]; then
    GATEWAY_IP=$(ping -c1 -W1 gateway 2>/dev/null | head -1 | sed -n 's/.*(\([0-9.]*\)).*/\1/p')
fi
if [ -z "$GATEWAY_IP" ]; then
    echo "[entrypoint] ERROR: could not resolve gateway IP" >&2
    exit 1
fi
echo "[entrypoint] gateway IP: $GATEWAY_IP"

# Redirect outbound HTTPS traffic to the MITM proxy.
# Exclude traffic destined for the gateway itself to avoid loops.
iptables -t nat -A OUTPUT -p tcp --dport 443 ! -d "$GATEWAY_IP" -j DNAT --to-destination "${GATEWAY_IP}:8443"
echo "[entrypoint] iptables: TCP 443 → ${GATEWAY_IP}:8443"

# Install the gateway CA certificate so TLS verification succeeds.
if [ -f /shared/certs/ca.crt ]; then
    cp /shared/certs/ca.crt /usr/local/share/ca-certificates/gateway-ca.crt
    update-ca-certificates --fresh >/dev/null 2>&1
    echo "[entrypoint] CA certificate installed"
else
    echo "[entrypoint] WARNING: CA cert not found at /shared/certs/ca.crt" >&2
fi

exec "$@"
`

// EntrypointScript returns the transparent proxy bootstrap script content.
func EntrypointScript() string {
	return entrypointScript
}

// BuildDockerfile generates a Dockerfile string from config and plugin contributions.
// The Dockerfile uses entrypoint.sh (expected alongside it in the build context) as
// ENTRYPOINT, which sets up iptables routing before handing off to CMD.
func BuildDockerfile(cfg *config.V1Config, contribs *plugin.Contributions) (string, error) {
	var lines []string

	// Base image
	baseImage := cfg.Runtime.Image
	var presetInstalls []string
	if preset, ok := Presets[cfg.Runtime.Image]; ok {
		baseImage = preset.BaseImage
		presetInstalls = preset.Installs
	}
	lines = append(lines, fmt.Sprintf("FROM %s", baseImage))
	lines = append(lines, "")

	// Preset installs (includes iptables for builtin presets)
	for _, inst := range presetInstalls {
		lines = append(lines, fmt.Sprintf("RUN %s", inst))
	}
	if len(presetInstalls) > 0 {
		lines = append(lines, "")
	}

	// For custom images that don't use a preset, install iptables explicitly.
	if _, isPreset := Presets[cfg.Runtime.Image]; !isPreset {
		lines = append(lines, "RUN apt-get update && apt-get install -y --no-install-recommends iptables iputils-ping ca-certificates wget && rm -rf /var/lib/apt/lists/*")
		lines = append(lines, "")
	}

	// User extra builds
	lines = append(lines, cfg.Runtime.ExtraBuilds...)
	if len(cfg.Runtime.ExtraBuilds) > 0 {
		lines = append(lines, "")
	}

	// Plugin extra builds
	if contribs != nil {
		lines = append(lines, contribs.Runtime.ExtraBuilds...)
		if len(contribs.Runtime.ExtraBuilds) > 0 {
			lines = append(lines, "")
		}
	}

	// Transparent proxy entrypoint wrapper
	lines = append(lines, "COPY .build/entrypoint.sh /usr/local/bin/entrypoint.sh")
	lines = append(lines, "RUN chmod +x /usr/local/bin/entrypoint.sh")
	lines = append(lines, `ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]`)
	lines = append(lines, "")

	// CMD — the actual agent process (passed as "$@" to entrypoint.sh)
	if len(cfg.Runtime.Entrypoint) > 0 {
		ep, err := json.Marshal(cfg.Runtime.Entrypoint)
		if err != nil {
			return "", fmt.Errorf("marshal entrypoint: %w", err)
		}
		lines = append(lines, fmt.Sprintf("CMD %s", string(ep)))
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n"), nil
}
