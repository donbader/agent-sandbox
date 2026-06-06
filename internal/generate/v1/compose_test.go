package v1

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCompose(t *testing.T) {
	cfg := &config.Config{
		Name: "test-agent",
		Runtime: config.RuntimeConfig{
			Volumes: []string{"data:/opt/data"},
		},
	}

	contribs := &plugin.Contributions{
		Sidecar: plugin.SidecarContrib{
			Services: map[string]plugin.ComposeService{
				"telegram": {
					Build:       "/project/sidecar",
					Environment: map[string]string{"AGENT_URL": "http://agent:8080"},
				},
			},
		},
	}

	output, err := BuildCompose(cfg, contribs, "/project")
	require.NoError(t, err)

	// Agent service uses config name
	assert.Contains(t, output, "test-agent:")
	assert.Contains(t, output, "data:/opt/data")

	// Gateway service uses config name + "-gateway"
	assert.Contains(t, output, "test-agent-gateway:")

	// Sidecar present with relative path from .build/
	assert.Contains(t, output, "telegram:")
	assert.Contains(t, output, "AGENT_URL")
	assert.Contains(t, output, "../sidecar")
}

func TestBuildCompose_NoSidecars(t *testing.T) {
	cfg := &config.Config{
		Name: "simple-agent",
		Runtime: config.RuntimeConfig{
			Image: "@builtin/codex",
		},
	}

	output, err := BuildCompose(cfg, nil, "/project")
	require.NoError(t, err)

	assert.Contains(t, output, "simple-agent:")
	assert.Contains(t, output, "simple-agent-gateway:")
	assert.NotContains(t, output, "telegram:")
}

func TestBuildCompose_PluginPorts(t *testing.T) {
	cfg := &config.Config{
		Name: "ssh-agent",
		Runtime: config.RuntimeConfig{
			Image: "@builtin/codex",
		},
	}

	contribs := &plugin.Contributions{
		Runtime: plugin.RuntimeContrib{
			Ports: []string{"2222:2222"},
		},
		Sidecar: plugin.SidecarContrib{Services: map[string]plugin.ComposeService{}},
	}

	output, err := BuildCompose(cfg, contribs, "/project")
	require.NoError(t, err)

	assert.Contains(t, output, "2222:2222")
}

func TestBuildCompose_CapDrop(t *testing.T) {
	cfg := &config.Config{
		Name: "secure-agent",
		Runtime: config.RuntimeConfig{
			Image: "@builtin/codex",
		},
	}

	output, err := BuildCompose(cfg, nil, "/project")
	require.NoError(t, err)

	// Both agent and gateway should drop all capabilities
	assert.Contains(t, output, "cap_drop:")
	assert.Contains(t, output, "- ALL")
	// Agent needs NET_ADMIN for iptables + user switching caps
	assert.Contains(t, output, "- NET_ADMIN")
	assert.Contains(t, output, "- SETUID")
	assert.Contains(t, output, "- SETGID")
	// Gateway needs NET_BIND_SERVICE for port 53
	assert.Contains(t, output, "- NET_BIND_SERVICE")
}

func TestBuildCompose_PodmanUserns(t *testing.T) {
	cfg := &config.Config{
		Name:          "podman-agent",
		RuntimeEngine: "podman",
		Runtime: config.RuntimeConfig{
			Image: "@builtin/codex",
		},
	}

	output, err := BuildCompose(cfg, nil, "/project")
	require.NoError(t, err)

	assert.Contains(t, output, "userns_mode: keep-id")
}

func TestBuildCompose_DockerNoUserns(t *testing.T) {
	cfg := &config.Config{
		Name: "docker-agent",
		Runtime: config.RuntimeConfig{
			Image: "@builtin/codex",
		},
	}

	output, err := BuildCompose(cfg, nil, "/project")
	require.NoError(t, err)

	assert.NotContains(t, output, "userns_mode")
}
