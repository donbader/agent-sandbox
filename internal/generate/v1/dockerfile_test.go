package v1

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildDockerfile(t *testing.T) {
	cfg := &config.V1Config{
		Runtime: config.RuntimeConfig{
			Image:       "node:24-slim",
			ExtraBuilds: []string{"RUN apt-get update && apt-get install -y git"},
			Entrypoint:  []string{"codex-acp", "--listen", ":8080"},
		},
	}

	contribs := &plugin.Contributions{
		Runtime: plugin.RuntimeContrib{
			ExtraBuilds: []string{"RUN npm install -g some-tool"},
		},
	}

	output, err := BuildDockerfile(cfg, contribs)
	require.NoError(t, err)

	assert.Contains(t, output, "FROM node:24-slim")
	assert.Contains(t, output, "RUN apt-get update && apt-get install -y git")
	assert.Contains(t, output, "RUN npm install -g some-tool")
	assert.Contains(t, output, `CMD ["codex-acp","--listen",":8080"]`)
	assert.Contains(t, output, "COPY .build/entrypoint.sh")
}

func TestBuildDockerfile_BuiltinPreset(t *testing.T) {
	cfg := &config.V1Config{
		Runtime: config.RuntimeConfig{
			Image:      "@builtin/codex",
			Entrypoint: []string{"sleep", "infinity"},
		},
	}

	output, err := BuildDockerfile(cfg, nil)
	require.NoError(t, err)

	assert.Contains(t, output, "FROM node:24-slim")
	assert.Contains(t, output, "npm install -g @openai/codex")
	assert.Contains(t, output, `CMD ["sleep","infinity"]`)
}

func TestBuildDockerfile_CustomImage(t *testing.T) {
	cfg := &config.V1Config{
		Runtime: config.RuntimeConfig{
			Image:      "python:3.12-slim",
			Entrypoint: []string{"python", "main.py"},
		},
	}

	output, err := BuildDockerfile(cfg, nil)
	require.NoError(t, err)

	assert.Contains(t, output, "FROM python:3.12-slim")
	assert.Contains(t, output, `CMD ["python","main.py"]`)
	assert.NotContains(t, output, "npm install")
}
