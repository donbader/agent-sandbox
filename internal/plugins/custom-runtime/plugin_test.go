package customruntime

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve(t *testing.T) {
	t.Run("full config", func(t *testing.T) {
		config := map[string]any{
			"commands":         []any{"apt-get install -y ripgrep", "apt-get install -y jq"},
			"entrypoint_hooks": []any{"scripts/setup.sh", "scripts/init.sh"},
			"runtime_volumes":  []any{"agent-home:/home/agent"},
			"home_override":    "./home",
			"env":              []any{"MY_API_KEY", "GITHUB_TOKEN"},
		}

		contrib, err := resolve.ResolveFeature("/project", "custom-runtime", config)
		require.NoError(t, err)
		assert.Equal(t, []string{"apt-get install -y ripgrep", "apt-get install -y jq"}, contrib.Commands)
		assert.Equal(t, []string{"scripts/setup.sh", "scripts/init.sh"}, contrib.EntrypointHooks)
		assert.Equal(t, []string{"agent-home:/home/agent"}, contrib.Volumes)
		assert.Equal(t, "./home", contrib.HomeOverride)
		assert.Equal(t, []string{"MY_API_KEY", "GITHUB_TOKEN"}, contrib.EnvVars)
	})

	t.Run("empty config", func(t *testing.T) {
		contrib, err := resolve.ResolveFeature("/project", "custom-runtime", map[string]any{})
		require.NoError(t, err)
		assert.Nil(t, contrib.Commands)
		assert.Nil(t, contrib.EntrypointHooks)
		assert.Nil(t, contrib.Volumes)
		assert.Equal(t, "", contrib.HomeOverride)
		assert.Nil(t, contrib.EnvVars)
	})

	t.Run("partial config", func(t *testing.T) {
		config := map[string]any{
			"commands": []any{"npm install -g typescript"},
		}

		contrib, err := resolve.ResolveFeature("/project", "custom-runtime", config)
		require.NoError(t, err)
		assert.Equal(t, []string{"npm install -g typescript"}, contrib.Commands)
		assert.Nil(t, contrib.EntrypointHooks)
		assert.Nil(t, contrib.Volumes)
		assert.Equal(t, "", contrib.HomeOverride)
	})
}
