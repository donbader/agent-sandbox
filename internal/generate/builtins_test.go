package generate

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
)

func TestResolveBuiltins(t *testing.T) {
	runtime := &resolve.RuntimeConfig{User: "agent"}

	t.Run("resolves AGENT_HOME", func(t *testing.T) {
		result := resolveBuiltins("data:/home/${AGENT_HOME}/stuff", runtime)
		// Note: ${AGENT_HOME} resolves to /home/agent, so this becomes data:/home//home/agent/stuff
		// The correct usage is: "data:${AGENT_HOME}" → "data:/home/agent"
		result = resolveBuiltins("agent-home:${AGENT_HOME}", runtime)
		assert.Equal(t, "agent-home:/home/agent", result)
	})

	t.Run("resolves AGENT_USER", func(t *testing.T) {
		result := resolveBuiltins("chown ${AGENT_USER}:${AGENT_USER} /data", runtime)
		assert.Equal(t, "chown agent:agent /data", result)
	})

	t.Run("resolves with custom user", func(t *testing.T) {
		customRuntime := &resolve.RuntimeConfig{User: "coder"}
		result := resolveBuiltins("vol:${AGENT_HOME}", customRuntime)
		assert.Equal(t, "vol:/home/coder", result)
	})

	t.Run("leaves non-builtin vars untouched", func(t *testing.T) {
		result := resolveBuiltins("${MY_API_KEY}", runtime)
		assert.Equal(t, "${MY_API_KEY}", result)
	})
}

func TestBuiltinNames(t *testing.T) {
	names := builtinNames()
	assert.True(t, names["AGENT_HOME"])
	assert.True(t, names["AGENT_USER"])
	assert.False(t, names["MY_CUSTOM_VAR"])
}

func TestResolveFeatureBuiltins(t *testing.T) {
	g := &Generator{
		Runtime: &resolve.RuntimeConfig{User: "agent"},
		Features: []*resolve.FeatureContributions{
			{
				Commands: []string{"mkdir -p ${AGENT_HOME}/.config"},
				Volumes:  []string{"agent-home:${AGENT_HOME}"},
			},
		},
	}

	g.resolveFeatureBuiltins()

	assert.Equal(t, []string{"mkdir -p /home/agent/.config"}, g.Features[0].Commands)
	assert.Equal(t, []string{"agent-home:/home/agent"}, g.Features[0].Volumes)
}
