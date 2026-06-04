package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadV1_BasicConfig(t *testing.T) {
	dir := t.TempDir()
	yaml := `
name: test-agent
log_level: debug
core_version: v1.0.0
runtime:
  image: "@builtin/codex"
  extra_builds:
    - "RUN apt-get install -y jq"
  entrypoint: ["codex-acp", "--listen", ":8080"]
  volumes:
    - "data:/opt/data"
gateway:
  services:
    - url: https://api.example.com
      headers:
        Authorization: Bearer ${TOKEN}
installations:
  - plugin: github-pat
    options:
      token: "${GITHUB_PAT}"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0644))

	cfg, err := LoadV1(dir)
	require.NoError(t, err)

	assert.Equal(t, "test-agent", cfg.Name)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, "v1.0.0", cfg.CoreVersion)
	assert.Equal(t, "@builtin/codex", cfg.Runtime.Image)
	assert.Equal(t, []string{"codex-acp", "--listen", ":8080"}, cfg.Runtime.Entrypoint)
	assert.Len(t, cfg.Gateway.Services, 1)
	assert.Equal(t, "https://api.example.com", cfg.Gateway.Services[0].URL)
	assert.Len(t, cfg.Installations, 1)
	assert.Equal(t, "github-pat", cfg.Installations[0].Plugin)
}
