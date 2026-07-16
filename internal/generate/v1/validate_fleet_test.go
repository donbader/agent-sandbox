package v1

import (
	"bytes"
	"os"
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateFleet_PortConflict_Ingress(t *testing.T) {
	entries := []ComposeAgentEntry{
		{
			Config: &config.Config{Name: "claude-agent"},
			Contribs: &plugin.Contributions{
				Gateway: plugin.GatewayContrib{
					Ingress: []plugin.IngressRule{
						{Listen: "8766", Target: "agent:8766"},
					},
				},
			},
		},
		{
			Config: &config.Config{Name: "codex-agent"},
			Contribs: &plugin.Contributions{
				Gateway: plugin.GatewayContrib{
					Ingress: []plugin.IngressRule{
						{Listen: "8766", Target: "agent:8766"},
					},
				},
			},
		},
	}

	err := validateFleet(entries)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "port conflict")
	assert.Contains(t, err.Error(), "8766")
	assert.Contains(t, err.Error(), "claude-agent")
	assert.Contains(t, err.Error(), "codex-agent")
}

func TestValidateFleet_PortConflict_PublishedPorts(t *testing.T) {
	entries := []ComposeAgentEntry{
		{
			Config: &config.Config{Name: "claude-agent"},
			Contribs: &plugin.Contributions{
				Gateway: plugin.GatewayContrib{
					PublishedPorts: []string{"9080:9080"},
				},
			},
		},
		{
			Config: &config.Config{Name: "codex-agent"},
			Contribs: &plugin.Contributions{
				Gateway: plugin.GatewayContrib{
					PublishedPorts: []string{"9080:9081"},
				},
			},
		},
	}

	err := validateFleet(entries)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "port conflict")
	assert.Contains(t, err.Error(), "9080")
	assert.Contains(t, err.Error(), "claude-agent")
	assert.Contains(t, err.Error(), "codex-agent")
}

func TestValidateFleet_PortConflict_CrossType(t *testing.T) {
	entries := []ComposeAgentEntry{
		{
			Config: &config.Config{Name: "claude-agent"},
			Contribs: &plugin.Contributions{
				Gateway: plugin.GatewayContrib{
					Ingress: []plugin.IngressRule{
						{Listen: "9080", Target: "agent:22"},
					},
				},
			},
		},
		{
			Config: &config.Config{Name: "codex-agent"},
			Contribs: &plugin.Contributions{
				Gateway: plugin.GatewayContrib{
					PublishedPorts: []string{"9080:8080"},
				},
			},
		},
	}

	err := validateFleet(entries)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "port conflict")
	assert.Contains(t, err.Error(), "9080")
	assert.Contains(t, err.Error(), "claude-agent")
	assert.Contains(t, err.Error(), "codex-agent")
}

func TestValidateFleet_NoConflict(t *testing.T) {
	entries := []ComposeAgentEntry{
		{
			Config: &config.Config{Name: "claude-agent"},
			Contribs: &plugin.Contributions{
				Gateway: plugin.GatewayContrib{
					Ingress: []plugin.IngressRule{
						{Listen: "8766", Target: "agent:8766"},
					},
					PublishedPorts: []string{"9080:9080"},
				},
			},
		},
		{
			Config: &config.Config{Name: "codex-agent"},
			Contribs: &plugin.Contributions{
				Gateway: plugin.GatewayContrib{
					Ingress: []plugin.IngressRule{
						{Listen: "8767", Target: "agent:8767"},
					},
					PublishedPorts: []string{"9081:9081"},
				},
			},
		},
	}

	err := validateFleet(entries)
	assert.NoError(t, err)
}

func TestValidateFleet_OAuthCallbackWarning(t *testing.T) {
	entries := []ComposeAgentEntry{
		{
			Config: &config.Config{Name: "codex-agent"},
			Contribs: &plugin.Contributions{
				Gateway: plugin.GatewayContrib{
					Routes: []plugin.RouteEntry{
						{Path: "/callback", Handler: "callback.ts"},
					},
					// No PublishedPorts — should trigger warning
				},
			},
		},
	}

	// Capture stderr output
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	err := validateFleet(entries)

	w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	buf.ReadFrom(r)
	stderr := buf.String()

	// Should not return an error (warnings only)
	assert.NoError(t, err)
	assert.Contains(t, stderr, "warning")
	assert.Contains(t, stderr, "codex-agent")
	assert.Contains(t, stderr, "OAuth callback route")
	assert.Contains(t, stderr, "no published port")
}

func TestValidateFleet_URLHostWarning(t *testing.T) {
	entries := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Name: "claude-agent",
				Gateway: config.GatewayConfig{
					Egress: []config.EgressRule{
						{Hosts: []string{"https://agw.playground.straitsx.ai/kiro/anthropic"}},
					},
				},
			},
			Contribs: &plugin.Contributions{},
		},
	}

	// Capture stderr output
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	err := validateFleet(entries)

	w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	buf.ReadFrom(r)
	stderr := buf.String()

	// Should not return an error (warnings only)
	assert.NoError(t, err)
	assert.Contains(t, stderr, "warning")
	assert.Contains(t, stderr, "claude-agent")
	assert.Contains(t, stderr, "https://agw.playground.straitsx.ai/kiro/anthropic")
	assert.Contains(t, stderr, "agw.playground.straitsx.ai")
}
