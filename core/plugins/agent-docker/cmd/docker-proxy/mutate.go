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
		// In compose mode: keep the requested network (it's one we created),
		// and additionally attach to the sandbox network for gateway routing.
		existingEndpoints := map[string]any{}
		if nc, ok := body["NetworkingConfig"].(map[string]any); ok {
			if ec, ok := nc["EndpointsConfig"].(map[string]any); ok {
				existingEndpoints = ec
			}
		}
		// Always add sandbox network
		existingEndpoints[m.cfg.NetworkName] = map[string]any{}
		body["NetworkingConfig"] = map[string]any{
			"EndpointsConfig": existingEndpoints,
		}
		// If no explicit NetworkMode was set, default to sandbox network
		if _, hasNM := hc["NetworkMode"]; !hasNM {
			hc["NetworkMode"] = m.cfg.NetworkName
		}
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
