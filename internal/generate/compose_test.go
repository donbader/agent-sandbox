package generate

import (
	"strings"
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposeBuilder_Single(t *testing.T) {
	t.Run("basic single service", func(t *testing.T) {
		g := &Generator{
			Config:  &config.AgentConfig{Name: "coder"},
			Runtime: &resolve.RuntimeConfig{Ports: []string{"1455:1455"}},
			Features: []*resolve.FeatureContributions{
				{Volumes: []string{"agent-home:/home/agent"}},
			},
		}

		cb := g.buildComposeBuilder()

		assert.Equal(t, "single", cb.Variant)
		assert.Equal(t, "coder", cb.AgentName)
		assert.Equal(t, []string{"1455:1455"}, cb.Ports)
		assert.Equal(t, []string{"agent-home:/home/agent"}, cb.Volumes)
		assert.Equal(t, []string{"agent-home"}, cb.NamedVolumes)
		assert.False(t, cb.HasMITM)
	})

	t.Run("renders with env vars", func(t *testing.T) {
		g := &Generator{
			Config: &config.AgentConfig{
				Name: "coder",
				Features: []config.FeatureEntry{
					{Plugin: "openai", Config: map[string]any{"api_key": "${OPENAI_API_KEY}"}},
					{Plugin: "telegram", Config: map[string]any{"bot_token": "${TELEGRAM_BOT_TOKEN}"}},
				},
			},
			Runtime: &resolve.RuntimeConfig{Ports: []string{"1455:1455"}},
			Features: []*resolve.FeatureContributions{
				{Volumes: []string{"agent-home:/home/agent"}},
			},
		}

		cb := g.buildComposeBuilder()
		content, err := renderTemplate("docker-compose.single.tmpl", cb)
		require.NoError(t, err)

		assert.Contains(t, content, "coder:")
		assert.Contains(t, content, "build:")
		assert.Contains(t, content, "1455:1455")
		assert.Contains(t, content, "agent-home:/home/agent")
		assert.Contains(t, content, "volumes:")
		assert.Contains(t, content, "  agent-home:")
		assert.Contains(t, content, "TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN}")
		assert.Contains(t, content, "OPENAI_API_KEY=${OPENAI_API_KEY}")
	})

	t.Run("no volumes when none configured", func(t *testing.T) {
		g := &Generator{
			Config:  &config.AgentConfig{Name: "coder"},
			Runtime: &resolve.RuntimeConfig{},
		}

		cb := g.buildComposeBuilder()
		content, err := renderTemplate("docker-compose.single.tmpl", cb)
		require.NoError(t, err)

		assert.Contains(t, content, "coder:")
		assert.NotContains(t, content, "  agent-home:")
	})
}

func TestComposeBuilder_Gateway(t *testing.T) {
	t.Run("basic gateway compose", func(t *testing.T) {
		g := &Generator{
			Config:  &config.AgentConfig{Name: "coder"},
			Runtime: &resolve.RuntimeConfig{},
			Gateway: true,
		}

		cb := g.buildComposeBuilder()

		assert.Equal(t, "gateway", cb.Variant)
		assert.Equal(t, "coder", cb.AgentName)
		assert.Equal(t, "coder-gateway", cb.GatewayName)
		assert.False(t, cb.HasMITM)
	})

	t.Run("renders with MITM", func(t *testing.T) {
		g := &Generator{
			Config: &config.AgentConfig{
				Name: "coder",
				Features: []config.FeatureEntry{
					{Plugin: "telegram", Config: map[string]any{"bot_token": "${TELEGRAM_BOT_TOKEN}"}},
				},
			},
			Runtime: &resolve.RuntimeConfig{},
			Features: []*resolve.FeatureContributions{
				{MITMDomains: []string{"api.telegram.org"}},
			},
			Gateway: true,
		}

		cb := g.buildComposeBuilder()
		content, err := renderTemplate("docker-compose.gateway.tmpl", cb)
		require.NoError(t, err)

		assert.Contains(t, content, "coder-gateway:")
		assert.Contains(t, content, "Dockerfile.gateway")
		assert.Contains(t, content, "coder:")
		assert.Contains(t, content, "Dockerfile.agent")
		assert.Contains(t, content, "NET_ADMIN")
		assert.Contains(t, content, "internal:")
		assert.Contains(t, content, "GATEWAY_HOST=coder-gateway")
		assert.Contains(t, content, "depends_on:")
		assert.Contains(t, content, "shared-certs:/shared/certs")
		assert.Contains(t, content, "shared-certs:/usr/local/share/ca-certificates:ro")
		assert.Contains(t, content, "service_healthy")
		assert.Contains(t, content, "TELEGRAM_BOT_TOKEN")

		// Verify shared-certs is not duplicated in named volumes
		assert.Equal(t, 1, strings.Count(content, "  shared-certs:"),
			"shared-certs should appear exactly once in named volumes section")
	})

	t.Run("without MITM uses simple depends_on", func(t *testing.T) {
		g := &Generator{
			Config:  &config.AgentConfig{Name: "coder"},
			Runtime: &resolve.RuntimeConfig{},
			Gateway: true,
		}

		cb := g.buildComposeBuilder()
		content, err := renderTemplate("docker-compose.gateway.tmpl", cb)
		require.NoError(t, err)

		assert.Contains(t, content, "depends_on:")
		assert.Contains(t, content, "- coder-gateway")
		assert.NotContains(t, content, "service_healthy")
		assert.NotContains(t, content, "shared-certs")
	})

	t.Run("agent env vars appear in agent service", func(t *testing.T) {
		g := &Generator{
			Config:  &config.AgentConfig{Name: "coder"},
			Runtime: &resolve.RuntimeConfig{},
			Features: []*resolve.FeatureContributions{
				{AgentEnv: []string{"GH_TOKEN=dummy"}},
			},
			Gateway: true,
		}

		cb := g.buildComposeBuilder()
		content, err := renderTemplate("docker-compose.gateway.tmpl", cb)
		require.NoError(t, err)

		assert.Contains(t, content, "GH_TOKEN=dummy")
	})
}
