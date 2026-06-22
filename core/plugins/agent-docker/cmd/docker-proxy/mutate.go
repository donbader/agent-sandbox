package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
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
		// In compose mode: keep the requested networks AND add the sandbox network.
		// Containers need the sandbox network to reach the gateway (DNS, MITM proxy).
		existingEndpoints := map[string]any{}
		if nc, ok := body["NetworkingConfig"].(map[string]any); ok {
			if ec, ok := nc["EndpointsConfig"].(map[string]any); ok {
				existingEndpoints = ec
			}
		}
		// Always add sandbox network for gateway reachability.
		existingEndpoints[m.cfg.NetworkName] = map[string]any{}
		body["NetworkingConfig"] = map[string]any{
			"EndpointsConfig": existingEndpoints,
		}
		// Set NetworkMode to sandbox only for standalone docker run (no compose networks).
		// For compose, Docker sets NetworkMode from the compose file.
		if len(existingEndpoints) == 1 {
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
// Uses the gateway-authored routing script (cached at startup) as the entrypoint wrapper.
func (m *Mutator) injectInitWrapper(body map[string]any, hc map[string]any) {
	// Add NET_ADMIN capability for ip route manipulation
	capAdd, _ := hc["CapAdd"].([]any)
	capAdd = append(capAdd, "NET_ADMIN")
	hc["CapAdd"] = capAdd

	// Set DNS to gateway IP so containers resolve through the gateway
	hc["Dns"] = []string{m.cfg.GatewayIP}

	// Mount certs volume (read-only) for CA cert access
	mounts, _ := hc["Mounts"].([]any)
	mounts = append(mounts, map[string]any{
		"Type":     "volume",
		"Source":   m.certsVolumeName(),
		"Target":   "/shared/certs",
		"ReadOnly": true,
	})
	hc["Mounts"] = mounts

	// Use the cached gateway route script content + exec "$@" as init command
	initCmd := m.cfg.GatewayRouteScript + "\nexec \"$@\""

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
		// No explicit entrypoint/cmd in create request.
		// Remove Cmd so Docker uses the image's default CMD as args to exec "$@".
		delete(body, "Cmd")
	}
}

// certsVolumeName returns the Docker volume name for the certs volume.
// Compose names volumes as {projectName}_{volumeName}. The volume is declared as
// {agentName}-certs in the compose file.
func (m *Mutator) certsVolumeName() string {
	projectName := strings.TrimSuffix(m.cfg.NetworkName, "_sandbox")
	return projectName + "_" + m.cfg.AgentName + "-certs"
}
