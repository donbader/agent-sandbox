package generate

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
)

func TestResolveBuiltins(t *testing.T) {
	runtime := &resolve.RuntimeConfig{User: "agent"}

	t.Run("resolves AGENT_HOME", func(t *testing.T) {
		result := resolveBuiltins("agent-home:{{ .AGENT_HOME }}", runtime)
		assert.Equal(t, "agent-home:/home/agent", result)
	})

	t.Run("resolves AGENT_USER", func(t *testing.T) {
		result := resolveBuiltins("chown {{ .AGENT_USER }}:{{ .AGENT_USER }} /data", runtime)
		assert.Equal(t, "chown agent:agent /data", result)
	})

	t.Run("resolves with custom user", func(t *testing.T) {
		customRuntime := &resolve.RuntimeConfig{User: "coder"}
		result := resolveBuiltins("vol:{{ .AGENT_HOME }}", customRuntime)
		assert.Equal(t, "vol:/home/coder", result)
	})

	t.Run("leaves runtime env vars untouched", func(t *testing.T) {
		result := resolveBuiltins("${MY_API_KEY}", runtime)
		assert.Equal(t, "${MY_API_KEY}", result)
	})

	t.Run("mixed builtins and env vars", func(t *testing.T) {
		result := resolveBuiltins("{{ .AGENT_HOME }}/.config/${APP_NAME}", runtime)
		assert.Equal(t, "/home/agent/.config/${APP_NAME}", result)
	})
}

func TestResolveFeatureBuiltins(t *testing.T) {
	g := &Generator{
		Runtime: &resolve.RuntimeConfig{User: "agent"},
		Features: []*resolve.FeatureContributions{
			{
				Commands: []string{"mkdir -p {{ .AGENT_HOME }}/.config"},
				Volumes:  []string{"agent-home:{{ .AGENT_HOME }}"},
			},
		},
	}

	g.resolveFeatureBuiltins()

	assert.Equal(t, []string{"mkdir -p /home/agent/.config"}, g.Features[0].Commands)
	assert.Equal(t, []string{"agent-home:/home/agent"}, g.Features[0].Volumes)
}
