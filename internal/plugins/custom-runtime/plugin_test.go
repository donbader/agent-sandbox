package customruntime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlugin_Name(t *testing.T) {
	p := &Plugin{}
	assert.Equal(t, "custom-runtime", p.Name())
}

func TestPlugin_Resolve(t *testing.T) {
	p := &Plugin{}

	t.Run("full config", func(t *testing.T) {
		config := map[string]any{
			"commands":         []any{"apt-get install -y ripgrep", "apt-get install -y jq"},
			"entrypoint_hooks": []any{"scripts/setup.sh", "scripts/init.sh"},
			"runtime_volumes":  []any{"agent-home:/home/agent"},
			"home_override":    "./home",
		}

		contrib, err := p.Resolve("/project", config)
		require.NoError(t, err)
		assert.Equal(t, []string{"apt-get install -y ripgrep", "apt-get install -y jq"}, contrib.Commands)
		assert.Equal(t, []string{"scripts/setup.sh", "scripts/init.sh"}, contrib.EntrypointHooks)
		assert.Equal(t, []string{"agent-home:/home/agent"}, contrib.Volumes)
		assert.Equal(t, "./home", contrib.HomeOverride)
	})

	t.Run("empty config", func(t *testing.T) {
		contrib, err := p.Resolve("/project", map[string]any{})
		require.NoError(t, err)
		assert.Nil(t, contrib.Commands)
		assert.Nil(t, contrib.EntrypointHooks)
		assert.Nil(t, contrib.Volumes)
		assert.Equal(t, "", contrib.HomeOverride)
	})

	t.Run("partial config", func(t *testing.T) {
		config := map[string]any{
			"commands": []any{"npm install -g typescript"},
		}

		contrib, err := p.Resolve("/project", config)
		require.NoError(t, err)
		assert.Equal(t, []string{"npm install -g typescript"}, contrib.Commands)
		assert.Nil(t, contrib.EntrypointHooks)
		assert.Nil(t, contrib.Volumes)
		assert.Equal(t, "", contrib.HomeOverride)
	})
}
