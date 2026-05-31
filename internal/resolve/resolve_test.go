package resolve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRuntime(t *testing.T) {
	t.Run("resolves from local plugins dir", func(t *testing.T) {
		dir := t.TempDir()
		pluginDir := filepath.Join(dir, "ext", "plugins", "custom")
		require.NoError(t, os.MkdirAll(pluginDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "runtime.yaml"), []byte(`
name: custom
base_image: python:3.12-slim
install:
  - pip install my-cli
cmd: ["my-cli", "run"]
`), 0644))

		rc, err := ResolveRuntime(dir, "custom")
		require.NoError(t, err)
		assert.Equal(t, "custom", rc.Name)
		assert.Equal(t, "python:3.12-slim", rc.BaseImage)
		assert.Equal(t, []string{"pip install my-cli"}, rc.Install)
		assert.Equal(t, []string{"my-cli", "run"}, rc.Cmd)
		assert.Equal(t, "agent", rc.User) // default
	})

	t.Run("resolves embedded codex", func(t *testing.T) {
		rc, err := ResolveRuntime("/nonexistent", "codex")
		require.NoError(t, err)
		assert.Equal(t, "codex", rc.Name)
		assert.Equal(t, "node:22-slim", rc.BaseImage)
		assert.Contains(t, rc.Install[len(rc.Install)-1], "codex")
		assert.Equal(t, []string{"sleep", "infinity"}, rc.Cmd)
	})

	t.Run("local overrides embedded", func(t *testing.T) {
		dir := t.TempDir()
		pluginDir := filepath.Join(dir, "ext", "plugins", "codex")
		require.NoError(t, os.MkdirAll(pluginDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "runtime.yaml"), []byte(`
name: codex
base_image: node:20-slim
install:
  - npm install -g @openai/codex@0.1.0
cmd: ["codex", "exec", "--full-auto"]
`), 0644))

		rc, err := ResolveRuntime(dir, "codex")
		require.NoError(t, err)
		assert.Equal(t, "node:20-slim", rc.BaseImage) // local override wins
	})

	t.Run("unknown runtime", func(t *testing.T) {
		_, err := ResolveRuntime("/nonexistent", "unknown-runtime")
		assert.ErrorContains(t, err, "unknown runtime")
	})
}

func TestResolveFeature(t *testing.T) {
	// Register a test plugin using the generic Register function
	type testConfig struct {
		Commands        []string `yaml:"commands"`
		EntrypointHooks []string `yaml:"entrypoint_hooks"`
		RuntimeVolumes  []string `yaml:"runtime_volumes"`
		HomeOverride    string   `yaml:"home_override"`
	}
	Register("test-custom-runtime", func(_ string, cfg testConfig) (*FeatureContributions, error) {
		return &FeatureContributions{
			Commands:        cfg.Commands,
			EntrypointHooks: cfg.EntrypointHooks,
			Volumes:         cfg.RuntimeVolumes,
			HomeOverride:    cfg.HomeOverride,
		}, nil
	})
	t.Cleanup(func() { delete(registry, "test-custom-runtime") })

	t.Run("resolves via registered plugin", func(t *testing.T) {
		userConfig := map[string]any{
			"commands":         []any{"apt-get install -y ripgrep"},
			"entrypoint_hooks": []any{"scripts/setup.sh"},
			"runtime_volumes":  []any{"agent-home:/home/agent"},
			"home_override":    "home",
		}

		contrib, err := ResolveFeature("/any/dir", "test-custom-runtime", userConfig)
		require.NoError(t, err)
		assert.Equal(t, []string{"apt-get install -y ripgrep"}, contrib.Commands)
		assert.Equal(t, []string{"scripts/setup.sh"}, contrib.EntrypointHooks)
		assert.Equal(t, []string{"agent-home:/home/agent"}, contrib.Volumes)
		assert.Equal(t, "home", contrib.HomeOverride)
	})

	t.Run("unknown feature", func(t *testing.T) {
		_, err := ResolveFeature("/nonexistent", "unknown-feature", map[string]any{})
		assert.ErrorContains(t, err, "unknown feature")
	})

	t.Run("empty config", func(t *testing.T) {
		Register("minimal", func(_ string, cfg testConfig) (*FeatureContributions, error) {
			return &FeatureContributions{
				Commands:        cfg.Commands,
				EntrypointHooks: cfg.EntrypointHooks,
				Volumes:         cfg.RuntimeVolumes,
				HomeOverride:    cfg.HomeOverride,
			}, nil
		})
		t.Cleanup(func() { delete(registry, "minimal") })

		contrib, err := ResolveFeature("/any/dir", "minimal", map[string]any{})
		require.NoError(t, err)
		assert.Nil(t, contrib.Commands)
		assert.Nil(t, contrib.EntrypointHooks)
		assert.Nil(t, contrib.Volumes)
		assert.Equal(t, "", contrib.HomeOverride)
	})
}

func TestRegister(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
	}
	Register("test-register", func(_ string, c cfg) (*FeatureContributions, error) {
		return &FeatureContributions{BridgeChannel: c.Name}, nil
	})
	t.Cleanup(func() { delete(registry, "test-register") })

	t.Run("plugin is in registry", func(t *testing.T) {
		plugin, ok := registry["test-register"]
		require.True(t, ok)
		assert.Equal(t, "test-register", plugin.Name())
	})

	t.Run("ConfigType returns zero value", func(t *testing.T) {
		plugin := registry["test-register"]
		ct := plugin.ConfigType()
		assert.Equal(t, cfg{}, ct)
	})

	t.Run("Resolve unmarshals config", func(t *testing.T) {
		plugin := registry["test-register"]
		contrib, err := plugin.Resolve("/dir", map[string]any{"name": "hello"})
		require.NoError(t, err)
		assert.Equal(t, "hello", contrib.BridgeChannel)
	})

	t.Run("Resolve handles invalid config gracefully", func(t *testing.T) {
		// A map that can't unmarshal into the struct should still work
		// (yaml is lenient — unknown fields are ignored)
		plugin := registry["test-register"]
		contrib, err := plugin.Resolve("/dir", map[string]any{"unknown_field": 123})
		require.NoError(t, err)
		assert.Equal(t, "", contrib.BridgeChannel)
	})
}

func TestRegisteredPlugins(t *testing.T) {
	type cfg struct{}
	Register("test-listed", func(_ string, c cfg) (*FeatureContributions, error) {
		return &FeatureContributions{}, nil
	})
	t.Cleanup(func() { delete(registry, "test-listed") })

	plugins := RegisteredPlugins()
	_, ok := plugins["test-listed"]
	assert.True(t, ok)
}
