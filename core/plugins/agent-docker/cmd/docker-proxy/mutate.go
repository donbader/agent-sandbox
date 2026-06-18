package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Mutator applies forced values to container create requests.
type Mutator struct {
	cfg *ProxyConfig
}

// NewMutator creates a mutator with the given config.
func NewMutator(cfg *ProxyConfig) *Mutator {
	return &Mutator{cfg: cfg}
}

// MutateCreate applies all forced mutations to a container create body.
func (m *Mutator) MutateCreate(body map[string]any, containerName string) {
	// Force labels
	labels, ok := body["Labels"].(map[string]any)
	if !ok || labels == nil {
		labels = map[string]any{}
	}
	labels["agent-sandbox.agent"] = m.cfg.AgentName
	labels["agent-sandbox.sandbox"] = m.cfg.SandboxID
	body["Labels"] = labels

	// Force HostConfig
	hc, ok := body["HostConfig"].(map[string]any)
	if !ok || hc == nil {
		hc = map[string]any{}
	}
	hc["Memory"] = m.cfg.MemoryBytes
	hc["NanoCpus"] = m.cfg.NanoCPUs
	hc["PidsLimit"] = m.cfg.PidsLimit
	hc["RestartPolicy"] = map[string]any{"Name": "no"}

	if m.cfg.AllowCompose {
		// In compose mode: keep the requested networks as-is.
		// Don't inject the outer sandbox network (breaks DooD/nested scenarios).
		// Instead, inject transparent proxy init script that routes through
		// whatever gateway is discoverable on the container's network.
		existingEndpoints := map[string]any{}
		if nc, ok := body["NetworkingConfig"].(map[string]any); ok {
			if ec, ok := nc["EndpointsConfig"].(map[string]any); ok {
				existingEndpoints = ec
			}
		}
		// Only inject sandbox network if container has NO networks specified
		// (standalone docker run, not compose)
		if len(existingEndpoints) == 0 {
			existingEndpoints[m.cfg.NetworkName] = map[string]any{}
			body["NetworkingConfig"] = map[string]any{
				"EndpointsConfig": existingEndpoints,
			}
			hc["NetworkMode"] = m.cfg.NetworkName
		}

		// Inject transparent proxy init wrapper
		m.injectInitWrapper(body, hc)
	} else {
		// Standard mode: force everything onto sandbox network only
		hc["NetworkMode"] = m.cfg.NetworkName
		body["NetworkingConfig"] = map[string]any{
			"EndpointsConfig": map[string]any{
				m.cfg.NetworkName: map[string]any{},
			},
		}
	}

	body["HostConfig"] = hc
}

// NamespaceContainerName prefixes a container name with sandbox identity.
func (m *Mutator) NamespaceContainerName(name string) string {
	prefix := fmt.Sprintf("%s-", m.cfg.SandboxID)
	if name == "" {
		name = randomSuffix()
	}
	return prefix + name
}

func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// injectInitWrapper adds transparent proxy setup to a spawned container.
// Inlines iptables DNAT setup into the entrypoint — no external files or volumes needed.
func (m *Mutator) injectInitWrapper(body map[string]any, hc map[string]any) {
	// Add NET_ADMIN capability for iptables
	capAdd, _ := hc["CapAdd"].([]any)
	capAdd = append(capAdd, "NET_ADMIN")
	hc["CapAdd"] = capAdd

	// Add gateway hostname env var
	gatewayHost := m.cfg.AgentName + "-gateway"
	env, _ := body["Env"].([]any)
	env = append(env, "SANDBOX_GATEWAY_HOST="+gatewayHost)
	body["Env"] = env

	// Inline transparent proxy setup as entrypoint wrapper.
	// Resolves gateway IP, sets up iptables DNAT, configures DNS, then execs original cmd.
	initCmd := `GW=$(getent hosts "$SANDBOX_GATEWAY_HOST" 2>/dev/null | awk '{print $1}' | head -1); ` +
		`if [ -z "$GW" ]; then GW=$(ping -c1 -W2 "$SANDBOX_GATEWAY_HOST" 2>/dev/null | head -1 | sed -n 's/.*\(\([0-9.]*\)\).*/\1/p'); fi; ` +
		`if [ -n "$GW" ]; then ` +
		`CIDR=$(ip route 2>/dev/null | grep "dev eth0" | grep -v default | awk '{print $1}' | head -1); ` +
		`[ -z "$CIDR" ] && CIDR="$GW/32"; ` +
		`iptables -t nat -A OUTPUT -p tcp ! -d "$CIDR" -j DNAT --to-destination "$GW:8443" 2>/dev/null || true; ` +
		`[ -w /etc/resolv.conf ] && printf 'nameserver %s\nnameserver 127.0.0.11\n' "$GW" > /etc/resolv.conf; ` +
		`fi; ` +
		`exec "$@"`

	// Collect original entrypoint + cmd into args for exec "$@"
	var originalCmd []any
	if ep, ok := body["Entrypoint"].([]any); ok && len(ep) > 0 {
		originalCmd = append(originalCmd, ep...)
	}
	if cmd, ok := body["Cmd"].([]any); ok && len(cmd) > 0 {
		originalCmd = append(originalCmd, cmd...)
	}

	// Set new entrypoint: sh -c "<init> ; exec $@" -- <original cmd...>
	body["Entrypoint"] = []any{"/bin/sh", "-c", initCmd, "--"}
	if len(originalCmd) > 0 {
		body["Cmd"] = originalCmd
	} else {
		body["Cmd"] = []any{"/bin/sh"}
	}
}
