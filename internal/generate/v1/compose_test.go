package v1

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCompose(t *testing.T) {
	cfg := &config.V1Config{
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
	cfg := &config.V1Config{
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
