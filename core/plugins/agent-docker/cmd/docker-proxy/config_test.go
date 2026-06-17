package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigFromEnv(t *testing.T) {
	// System-injected env vars (from generator)
	t.Setenv("SANDBOX_ID", "my-project-coder")
	t.Setenv("SANDBOX_NETWORK", "my-project_sandbox")
	t.Setenv("AGENT_NAME", "coder")
	// Plugin-contributed env vars
	t.Setenv("ALLOWED_IMAGES", `["node:20-*","python:3.12-*"]`)
	t.Setenv("MAX_CONTAINERS", "5")
	t.Setenv("MEMORY_LIMIT", "2g")
	t.Setenv("CPU_LIMIT", "2")
	t.Setenv("PID_LIMIT", "256")

	cfg, err := loadConfigFromEnv()
	require.NoError(t, err)

	assert.Equal(t, "my-project-coder", cfg.SandboxID)
	assert.Equal(t, "coder", cfg.AgentName)
	assert.Equal(t, "my-project_sandbox", cfg.NetworkName)
	assert.Equal(t, []string{"node:20-*", "python:3.12-*"}, cfg.AllowedImages)
	assert.Equal(t, 5, cfg.MaxContainers)
	assert.Equal(t, int64(2*1024*1024*1024), cfg.MemoryBytes)
	assert.Equal(t, int64(2000000000), cfg.NanoCPUs)
	assert.Equal(t, int64(256), cfg.PidsLimit)
}

func TestLoadConfigFromEnv_MissingRequired(t *testing.T) {
	// Clear all env
	t.Setenv("SANDBOX_ID", "")
	t.Setenv("AGENT_NAME", "")
	t.Setenv("SANDBOX_NETWORK", "")
	t.Setenv("ALLOWED_IMAGES", "")

	_, err := loadConfigFromEnv()
	assert.Error(t, err)
}
