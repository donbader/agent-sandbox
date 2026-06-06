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

func TestBuildFleetCompose(t *testing.T) {
	agents := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Name: "coder",
				Runtime: config.RuntimeConfig{
					Image:   "@builtin/codex",
					Volumes: []string{"coder-data:/opt/data"},
				},
			},
			Contribs: &plugin.Contributions{
				Runtime: plugin.RuntimeContrib{
					Ports: []string{"8080:8080"},
				},
				Sidecar: plugin.SidecarContrib{Services: map[string]plugin.ComposeService{}},
			},
			BuildDir: "/project/.build/coder",
		},
		{
			Config: &config.Config{
				Name: "reviewer",
				Runtime: config.RuntimeConfig{
					Image: "@builtin/codex",
				},
			},
			Contribs: nil,
			BuildDir: "/project/.build/reviewer",
		},
	}

	output, err := BuildFleetCompose(agents, "/project")
	require.NoError(t, err)

	// Both agents present
	assert.Contains(t, output, "coder:")
	assert.Contains(t, output, "coder-gateway:")
	assert.Contains(t, output, "reviewer:")
	assert.Contains(t, output, "reviewer-gateway:")

	// Per-agent Dockerfile paths
	assert.Contains(t, output, ".build/coder/Dockerfile")
	assert.Contains(t, output, ".build/reviewer/Dockerfile")

	// Per-agent gateway config mount
	assert.Contains(t, output, "./coder/config.yaml:/etc/gateway/config.yaml:ro")
	assert.Contains(t, output, "./reviewer/config.yaml:/etc/gateway/config.yaml:ro")

	// Shared network
	assert.Contains(t, output, "sandbox:")

	// Named volumes
	assert.Contains(t, output, "coder-data:")
	assert.Contains(t, output, "certs:")

	// Ports from coder
	assert.Contains(t, output, "8080:8080")
}

func TestBuildFleetCompose_SidecarNamespacing(t *testing.T) {
	agents := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Name: "agent-a",
				Runtime: config.RuntimeConfig{
					Image: "@builtin/codex",
				},
			},
			Contribs: &plugin.Contributions{
				Sidecar: plugin.SidecarContrib{
					Services: map[string]plugin.ComposeService{
						"telegram": {
							Build:       "/project/plugins/telegram",
							Environment: map[string]string{"BOT": "a-bot"},
						},
					},
				},
			},
			BuildDir: "/project/.build/agent-a",
		},
		{
			Config: &config.Config{
				Name: "agent-b",
				Runtime: config.RuntimeConfig{
					Image: "@builtin/codex",
				},
			},
			Contribs: &plugin.Contributions{
				Sidecar: plugin.SidecarContrib{
					Services: map[string]plugin.ComposeService{
						"telegram": {
							Build:       "/project/plugins/telegram",
							Environment: map[string]string{"BOT": "b-bot"},
						},
					},
				},
			},
			BuildDir: "/project/.build/agent-b",
		},
	}

	output, err := BuildFleetCompose(agents, "/project")
	require.NoError(t, err)

	// Sidecars should be namespaced by agent name to avoid collisions
	assert.Contains(t, output, "agent-a-telegram:")
	assert.Contains(t, output, "agent-b-telegram:")
}
