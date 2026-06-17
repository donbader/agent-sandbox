package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMutateCreate(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:   "my-project-coder",
		AgentName:   "coder",
		NetworkName: "my-project_sandbox",
		MemoryBytes: 2 * 1024 * 1024 * 1024,
		NanoCPUs:    2000000000,
		PidsLimit:   256,
	}
	m := NewMutator(cfg)

	body := map[string]any{
		"Image": "node:20",
		"HostConfig": map[string]any{
			"NetworkMode": "bridge",
		},
	}

	m.MutateCreate(body, "my-app")

	// Check labels
	labels, _ := body["Labels"].(map[string]any)
	assert.Equal(t, "coder", labels["agent-sandbox.agent"])
	assert.Equal(t, "my-project-coder", labels["agent-sandbox.sandbox"])

	// Check host config
	hc, _ := body["HostConfig"].(map[string]any)
	assert.Equal(t, "my-project_sandbox", hc["NetworkMode"])
	assert.Equal(t, int64(2*1024*1024*1024), hc["Memory"])
	assert.Equal(t, int64(2000000000), hc["NanoCpus"])
	assert.Equal(t, int64(256), hc["PidsLimit"])
	rp, _ := hc["RestartPolicy"].(map[string]any)
	assert.Equal(t, "no", rp["Name"])
}

func TestMutateCreate_NamespaceContainerName(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:   "my-project-coder",
		AgentName:   "coder",
		NetworkName: "my-project_sandbox",
		MemoryBytes: 2 * 1024 * 1024 * 1024,
		NanoCPUs:    2000000000,
		PidsLimit:   256,
	}
	m := NewMutator(cfg)

	// With user-provided name
	name := m.NamespaceContainerName("my-postgres")
	assert.Equal(t, "my-project-coder-my-postgres", name)

	// Empty name gets random suffix
	name = m.NamespaceContainerName("")
	assert.True(t, len(name) > len("my-project-coder-"))
	assert.Contains(t, name, "my-project-coder-")
}
