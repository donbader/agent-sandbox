package v1

import (
	"path/filepath"
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

	agents := []ComposeAgentEntry{{
		Config:   cfg,
		Contribs: contribs,
		BuildDir: "/project/.build/test-agent",
	}}
	output, err := BuildProjectCompose(agents, "/project")
	require.NoError(t, err)

	// Agent service uses config name
	assert.Contains(t, output, "test-agent:")
	assert.Contains(t, output, "data:/opt/data")

	// Gateway service uses config name + "-gateway"
	assert.Contains(t, output, "test-agent-gateway:")

	// Sidecar present with agent name prefix and relative path from .build/
	assert.Contains(t, output, "test-agent-telegram:")
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

	agents := []ComposeAgentEntry{{
		Config:   cfg,
		Contribs: nil,
		BuildDir: "/project/.build/simple-agent",
	}}
	output, err := BuildProjectCompose(agents, "/project")
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

	agents := []ComposeAgentEntry{{
		Config:   cfg,
		Contribs: contribs,
		BuildDir: "/project/.build/ssh-agent",
	}}
	output, err := BuildProjectCompose(agents, "/project")
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

	agents := []ComposeAgentEntry{{
		Config:   cfg,
		Contribs: nil,
		BuildDir: "/project/.build/secure-agent",
	}}
	output, err := BuildProjectCompose(agents, "/project")
	require.NoError(t, err)

	// Both agent and gateway should drop all capabilities
	assert.Contains(t, output, "cap_drop:")
	assert.Contains(t, output, "- ALL")
	// Agent needs NET_ADMIN for iptables + user switching caps
	assert.Contains(t, output, "- NET_ADMIN")
	assert.Contains(t, output, "- SETUID")
	assert.Contains(t, output, "- SETGID")
	// SYS_CHROOT is NOT in base caps — it comes from plugin contributions
	assert.NotContains(t, output, "- SYS_CHROOT")
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

	agents := []ComposeAgentEntry{{
		Config:   cfg,
		Contribs: nil,
		BuildDir: "/project/.build/podman-agent",
	}}
	output, err := BuildProjectCompose(agents, "/project")
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

	agents := []ComposeAgentEntry{{
		Config:   cfg,
		Contribs: nil,
		BuildDir: "/project/.build/docker-agent",
	}}
	output, err := BuildProjectCompose(agents, "/project")
	require.NoError(t, err)

	assert.NotContains(t, output, "userns_mode")
}

func TestBuildCompose_PodmanSSHNoUserns(t *testing.T) {
	cfg := &config.Config{
		Name:          "podman-ssh-agent",
		RuntimeEngine: "podman",
		Runtime: config.RuntimeConfig{
			Image: "@builtin/codex",
		},
	}

	contribs := &plugin.Contributions{
		Runtime: plugin.RuntimeContrib{
			SkipUserns: true,
		},
		Sidecar: plugin.SidecarContrib{Services: map[string]plugin.ComposeService{}},
	}

	agents := []ComposeAgentEntry{{
		Config:   cfg,
		Contribs: contribs,
		BuildDir: "/project/.build/podman-ssh-agent",
	}}
	output, err := BuildProjectCompose(agents, "/project")
	require.NoError(t, err)

	// Plugin declares skip_userns — userns_mode must be skipped.
	assert.NotContains(t, output, "userns_mode")
}

func TestBuildCompose_PluginCapAdd(t *testing.T) {
	cfg := &config.Config{
		Name: "cap-agent",
		Runtime: config.RuntimeConfig{
			Image: "@builtin/codex",
		},
	}

	contribs := &plugin.Contributions{
		Runtime: plugin.RuntimeContrib{
			CapAdd: []string{"SYS_CHROOT", "SYS_PTRACE"},
		},
		Sidecar: plugin.SidecarContrib{Services: map[string]plugin.ComposeService{}},
	}

	agents := []ComposeAgentEntry{{
		Config:   cfg,
		Contribs: contribs,
		BuildDir: "/project/.build/cap-agent",
	}}
	output, err := BuildProjectCompose(agents, "/project")
	require.NoError(t, err)

	// Base caps present
	assert.Contains(t, output, "- NET_ADMIN")
	assert.Contains(t, output, "- SETUID")
	// Plugin-contributed caps present
	assert.Contains(t, output, "- SYS_CHROOT")
	assert.Contains(t, output, "- SYS_PTRACE")
}

func TestBuildCompose_PluginCapAddDedup(t *testing.T) {
	cfg := &config.Config{
		Name: "dedup-agent",
		Runtime: config.RuntimeConfig{
			Image: "@builtin/codex",
		},
	}

	// Plugin contributes a cap that already exists in base set — should not duplicate.
	contribs := &plugin.Contributions{
		Runtime: plugin.RuntimeContrib{
			CapAdd: []string{"NET_ADMIN", "SYS_CHROOT"},
		},
		Sidecar: plugin.SidecarContrib{Services: map[string]plugin.ComposeService{}},
	}

	agents := []ComposeAgentEntry{{
		Config:   cfg,
		Contribs: contribs,
		BuildDir: "/project/.build/dedup-agent",
	}}
	output, err := BuildProjectCompose(agents, "/project")
	require.NoError(t, err)

	var composed struct {
		Services map[string]struct {
			CapAdd []string `yaml:"cap_add"`
		} `yaml:"services"`
	}
	err = yaml.Unmarshal([]byte(output), &composed)
	require.NoError(t, err)

	agentCaps := composed.Services["dedup-agent"].CapAdd
	// NET_ADMIN appears exactly once in the agent's cap_add list.
	count := 0
	for _, c := range agentCaps {
		if c == "NET_ADMIN" {
			count++
		}
	}
	assert.Equal(t, 1, count, "NET_ADMIN should appear exactly once")
	// SYS_CHROOT contributed by plugin is present.
	assert.Contains(t, agentCaps, "SYS_CHROOT")
}

func TestBuildProjectCompose_SingleAgent(t *testing.T) {
	agents := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Name: "my-agent",
				Runtime: config.RuntimeConfig{
					Image:   "@builtin/codex",
					Volumes: []string{"data:/opt/data"},
				},
			},
			Contribs: nil,
			BuildDir: "/project/.build/my-agent",
		},
	}

	output, err := BuildProjectCompose(agents, "/project")
	require.NoError(t, err)

	// Agent and gateway services present
	assert.Contains(t, output, "my-agent:")
	assert.Contains(t, output, "my-agent-gateway:")

	// Gateway port not exposed to host
	var composed struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	err = yaml.Unmarshal([]byte(output), &composed)
	require.NoError(t, err)
	assert.NotContains(t, composed.Services["my-agent-gateway"].Ports, "8080")

	// Build paths are nested
	assert.Contains(t, output, ".build/my-agent/Dockerfile")
}

func TestBuildProjectCompose_MultipleAgents(t *testing.T) {
	agents := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Name: "coder",
				Runtime: config.RuntimeConfig{Image: "@builtin/codex"},
			},
			Contribs: nil,
			BuildDir: "/project/.build/coder",
		},
		{
			Config: &config.Config{
				Name: "reviewer",
				Runtime: config.RuntimeConfig{Image: "@builtin/codex"},
			},
			Contribs: nil,
			BuildDir: "/project/.build/reviewer",
		},
	}

	output, err := BuildProjectCompose(agents, "/project")
	require.NoError(t, err)

	assert.Contains(t, output, "coder:")
	assert.Contains(t, output, "coder-gateway:")
	assert.Contains(t, output, "reviewer:")
	assert.Contains(t, output, "reviewer-gateway:")

	// Both gateways do not expose port to host
	var composed struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	err = yaml.Unmarshal([]byte(output), &composed)
	require.NoError(t, err)
	assert.NotContains(t, composed.Services["coder-gateway"].Ports, "8080")
	assert.NotContains(t, composed.Services["reviewer-gateway"].Ports, "8080")
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

	output, err := BuildProjectCompose(agents, "/project")
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
	assert.Contains(t, output, "coder-certs:")

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

	output, err := BuildProjectCompose(agents, "/project")
	require.NoError(t, err)

	// Sidecars should be namespaced by agent name to avoid collisions
	assert.Contains(t, output, "agent-a-telegram:")
	assert.Contains(t, output, "agent-b-telegram:")
}

func TestBuildFleetCompose_SidecarBuildPath(t *testing.T) {
	// Sidecar build paths must be relative to .build/ (where docker-compose.yml lives),
	// not relative to .build/<agent-name>/ (the per-agent build dir).
	agents := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Name: "dorey-001",
				Runtime: config.RuntimeConfig{
					Image: "@builtin/codex",
				},
			},
			Contribs: &plugin.Contributions{
				Sidecar: plugin.SidecarContrib{
					Services: map[string]plugin.ComposeService{
						"telegram": {
							Build:       "/project/dorey-001/plugins/telegram/telegram-adapter",
							Environment: map[string]string{"BOT_TOKEN": "tok"},
						},
					},
				},
			},
			BuildDir: "/project/.build/dorey-001",
		},
	}

	output, err := BuildProjectCompose(agents, "/project")
	require.NoError(t, err)

	// Path from .build/docker-compose.yml to /project/dorey-001/plugins/telegram/telegram-adapter
	// should be ../dorey-001/plugins/telegram/telegram-adapter (one level up from .build/)
	assert.Contains(t, output, "../dorey-001/plugins/telegram/telegram-adapter")
	// Must NOT contain ../../ (two levels up — the bug)
	assert.NotContains(t, output, "../../dorey-001/plugins/telegram/telegram-adapter")
}

func TestBuildFleetCompose_PluginCapAdd(t *testing.T) {
	agents := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Name: "fleet-cap-agent",
				Runtime: config.RuntimeConfig{
					Image: "@builtin/codex",
				},
			},
			Contribs: &plugin.Contributions{
				Runtime: plugin.RuntimeContrib{
					CapAdd: []string{"SYS_CHROOT"},
				},
				Sidecar: plugin.SidecarContrib{Services: map[string]plugin.ComposeService{}},
			},
			BuildDir: "/project/.build/fleet-cap-agent",
		},
	}

	output, err := BuildProjectCompose(agents, "/project")
	require.NoError(t, err)

	var composed struct {
		Services map[string]struct {
			CapAdd []string `yaml:"cap_add"`
		} `yaml:"services"`
	}
	err = yaml.Unmarshal([]byte(output), &composed)
	require.NoError(t, err)

	agentCaps := composed.Services["fleet-cap-agent"].CapAdd
	assert.Contains(t, agentCaps, "SYS_CHROOT", "plugin-contributed cap should appear in fleet agent")
	assert.Contains(t, agentCaps, "NET_ADMIN", "base cap should still be present")
}

func TestBuildProjectCompose_TwoNetworkModel(t *testing.T) {
	agents := []ComposeAgentEntry{{
		Config: &config.Config{
			Name: "coder",
			Runtime: config.RuntimeConfig{
				CWD: "/home/agent/workspace",
			},
		},
		Contribs: &plugin.Contributions{},
		BuildDir: t.TempDir(),
	}}

	output, err := BuildProjectCompose(agents, t.TempDir())
	require.NoError(t, err)

	// Parse the output YAML
	var compose struct {
		Networks map[string]any `yaml:"networks"`
		Services map[string]any `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(output), &compose))

	// Verify two networks exist
	assert.Contains(t, compose.Networks, "sandbox")
	assert.Contains(t, compose.Networks, "external")

	// Verify sandbox is internal
	sandboxNet, ok := compose.Networks["sandbox"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, sandboxNet["internal"])

	// Verify gateway has both networks
	gw, ok := compose.Services["coder-gateway"].(map[string]any)
	require.True(t, ok)
	gwNetworks, ok := gw["networks"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, gwNetworks, "sandbox")
	assert.Contains(t, gwNetworks, "external")

	// Verify agent only has sandbox
	agent, ok := compose.Services["coder"].(map[string]any)
	require.True(t, ok)
	agentNetworks, ok := agent["networks"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, agentNetworks, "sandbox")
	assert.NotContains(t, agentNetworks, "external")
}

func TestBuildFleetCompose_SkipUserns(t *testing.T) {
	agents := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Name:          "fleet-podman-agent",
				RuntimeEngine: "podman",
				Runtime: config.RuntimeConfig{
					Image: "@builtin/codex",
				},
			},
			Contribs: &plugin.Contributions{
				Runtime: plugin.RuntimeContrib{
					SkipUserns: true,
				},
				Sidecar: plugin.SidecarContrib{Services: map[string]plugin.ComposeService{}},
			},
			BuildDir: "/project/.build/fleet-podman-agent",
		},
	}

	output, err := BuildProjectCompose(agents, "/project")
	require.NoError(t, err)

	// Plugin declares skip_userns on a podman engine — userns_mode must NOT appear.
	assert.NotContains(t, output, "userns_mode")
}

func TestBuildProjectCompose_SidecarSystemEnvVars(t *testing.T) {
	agents := []ComposeAgentEntry{{
		Config: &config.Config{
			Name: "coder",
			Runtime: config.RuntimeConfig{
				CWD: "/home/agent/workspace",
			},
		},
		Contribs: &plugin.Contributions{
			Sidecar: plugin.SidecarContrib{
				Services: map[string]plugin.ComposeService{
					"my-sidecar": {
						Image: "alpine:3.20",
					},
				},
			},
		},
		BuildDir: t.TempDir(),
	}}

	projectDir := t.TempDir()
	output, err := BuildProjectCompose(agents, projectDir)
	require.NoError(t, err)

	var compose struct {
		Services map[string]any `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(output), &compose))

	sidecar, ok := compose.Services["coder-my-sidecar"].(map[string]any)
	require.True(t, ok, "sidecar service not found")

	env, ok := sidecar["environment"].(map[string]any)
	require.True(t, ok, "environment not found or wrong type")

	projectName := filepath.Base(projectDir)
	assert.Equal(t, "coder", env["AGENT_NAME"])
	assert.Equal(t, projectName+"-coder", env["SANDBOX_ID"])
	assert.Equal(t, projectName+"_sandbox", env["SANDBOX_NETWORK"])
}
