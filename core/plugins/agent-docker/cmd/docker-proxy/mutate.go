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
	hc["NetworkMode"] = m.cfg.NetworkName
	hc["Memory"] = m.cfg.MemoryBytes
	hc["NanoCpus"] = m.cfg.NanoCPUs
	hc["PidsLimit"] = m.cfg.PidsLimit
	hc["RestartPolicy"] = map[string]any{"Name": "no"}
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
