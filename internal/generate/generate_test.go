package generate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/resolve"
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
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
			},
			Dir:    t.TempDir(),
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		// Check Dockerfile
		df, err := os.ReadFile(filepath.Join(outDir, "Dockerfile"))
		require.NoError(t, err)
		assert.Contains(t, string(df), "FROM node:22-slim")
		assert.Contains(t, string(df), "npm install -g @openai/codex")
		assert.Contains(t, string(df), "USER agent")
		assert.Contains(t, string(df), `CMD ["sleep", "infinity"]`)
		assert.NotContains(t, string(df), "ENTRYPOINT")

		// Check docker-compose.yml
		dc, err := os.ReadFile(filepath.Join(outDir, "docker-compose.yml"))
		require.NoError(t, err)
		assert.Contains(t, string(dc), "coder:")
		assert.Contains(t, string(dc), "build:")
		assert.Contains(t, string(dc), "container_name: coder")
	})

	t.Run("with feature commands", func(t *testing.T) {
		outDir := t.TempDir()
		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
			},
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
			},
			Features: []*resolve.FeatureContributions{
				{Commands: []string{"apt-get install -y ripgrep fd-find"}},
			},
			Dir:    t.TempDir(),
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		df, err := os.ReadFile(filepath.Join(outDir, "Dockerfile"))
		require.NoError(t, err)
		assert.Contains(t, string(df), "RUN apt-get install -y ripgrep fd-find")
	})

	t.Run("with entrypoint hooks", func(t *testing.T) {
		srcDir := t.TempDir()
		outDir := t.TempDir()

		// Create a hook script in the source dir
		scriptsDir := filepath.Join(srcDir, "scripts")
		require.NoError(t, os.MkdirAll(scriptsDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(scriptsDir, "setup.sh"), []byte("#!/bin/bash\necho setup"), 0755))

		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
			},
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
			},
			Features: []*resolve.FeatureContributions{
				{EntrypointHooks: []string{"scripts/setup.sh"}},
			},
			Dir:    srcDir,
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		// Dockerfile should have ENTRYPOINT instead of CMD
		df, err := os.ReadFile(filepath.Join(outDir, "Dockerfile"))
		require.NoError(t, err)
		assert.Contains(t, string(df), `ENTRYPOINT ["/opt/entrypoint.sh"]`)
		assert.NotContains(t, string(df), "CMD")
		assert.Contains(t, string(df), "COPY hooks/ /opt/hooks/")

		// Entrypoint script should exist
		ep, err := os.ReadFile(filepath.Join(outDir, "entrypoint.sh"))
		require.NoError(t, err)
		assert.Contains(t, string(ep), "/opt/hooks/setup.sh")
		assert.Contains(t, string(ep), "exec sleep infinity")

		// Hook should be copied
		hook, err := os.ReadFile(filepath.Join(outDir, "hooks", "setup.sh"))
		require.NoError(t, err)
		assert.Contains(t, string(hook), "echo setup")
	})

	t.Run("with home override", func(t *testing.T) {
		srcDir := t.TempDir()
		outDir := t.TempDir()

		// Create home override directory
		homeDir := filepath.Join(srcDir, "home")
		require.NoError(t, os.MkdirAll(homeDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(homeDir, ".gitconfig"), []byte("[user]\n  name = Agent"), 0644))

		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
			},
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
			},
			Features: []*resolve.FeatureContributions{
				{HomeOverride: "home"},
			},
			Dir:    srcDir,
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		// Dockerfile should COPY home-override
		df, err := os.ReadFile(filepath.Join(outDir, "Dockerfile"))
		require.NoError(t, err)
		assert.Contains(t, string(df), "COPY home-override/ /opt/home-override/")

		// Entrypoint should copy home override
		ep, err := os.ReadFile(filepath.Join(outDir, "entrypoint.sh"))
		require.NoError(t, err)
		assert.Contains(t, string(ep), "cp -rT /opt/home-override /home/agent")

		// Home override files should be copied
		gc, err := os.ReadFile(filepath.Join(outDir, "home-override", ".gitconfig"))
		require.NoError(t, err)
		assert.Contains(t, string(gc), "name = Agent")
	})

	t.Run("with volumes", func(t *testing.T) {
		outDir := t.TempDir()
		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
			},
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
			},
			Features: []*resolve.FeatureContributions{
				{Volumes: []string{"agent-home:/home/agent"}},
			},
			Dir:    t.TempDir(),
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		dc, err := os.ReadFile(filepath.Join(outDir, "docker-compose.yml"))
		require.NoError(t, err)
		assert.Contains(t, string(dc), "agent-home:/home/agent")
		assert.Contains(t, string(dc), "volumes:")
		assert.Contains(t, string(dc), "  agent-home:")
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
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
			},
			Dir:    t.TempDir(),
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		env, err := os.ReadFile(filepath.Join(outDir, ".env.example"))
		require.NoError(t, err)
		assert.Contains(t, string(env), "GITHUB_PAT=")
	})

	t.Run("no features no env", func(t *testing.T) {
		outDir := t.TempDir()
		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
			},
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
			},
			Dir:    t.TempDir(),
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		_, err = os.Stat(filepath.Join(outDir, ".env.example"))
		assert.True(t, os.IsNotExist(err))
	})
}
