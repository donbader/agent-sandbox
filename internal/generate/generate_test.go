package generate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/plugins/codex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerator_Run(t *testing.T) {
	t.Run("basic codex agent", func(t *testing.T) {
		outDir := t.TempDir()
		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
			},
			Runtime: codex.New(),
			Dir:     t.TempDir(),
			OutDir:  outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		// Check Dockerfile exists and has expected content
		df, err := os.ReadFile(filepath.Join(outDir, "Dockerfile"))
		require.NoError(t, err)
		assert.Contains(t, string(df), "FROM node:22-slim")
		assert.Contains(t, string(df), "npm install -g @openai/codex")
		assert.Contains(t, string(df), "USER agent")
		assert.Contains(t, string(df), `CMD ["sleep", "infinity"]`)

		// Check docker-compose.yml
		dc, err := os.ReadFile(filepath.Join(outDir, "docker-compose.yml"))
		require.NoError(t, err)
		assert.Contains(t, string(dc), "coder:")
		assert.Contains(t, string(dc), "build:")
		assert.Contains(t, string(dc), "container_name: coder")
	})

	t.Run("with env vars", func(t *testing.T) {
		outDir := t.TempDir()
		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
				Features: map[string]map[string]any{
					"github": {"token": "${GITHUB_PAT}"},
				},
			},
			Runtime: codex.New(),
			Dir:     t.TempDir(),
			OutDir:  outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		// Check .env.example
		env, err := os.ReadFile(filepath.Join(outDir, ".env.example"))
		require.NoError(t, err)
		assert.Contains(t, string(env), "GITHUB_PAT=")

		// Check docker-compose.yml has environment
		dc, err := os.ReadFile(filepath.Join(outDir, "docker-compose.yml"))
		require.NoError(t, err)
		assert.Contains(t, string(dc), "GITHUB_PAT")
	})

	t.Run("no features no env", func(t *testing.T) {
		outDir := t.TempDir()
		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
			},
			Runtime: codex.New(),
			Dir:     t.TempDir(),
			OutDir:  outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		// No .env.example when no env vars
		_, err = os.Stat(filepath.Join(outDir, ".env.example"))
		assert.True(t, os.IsNotExist(err))
	})
}
