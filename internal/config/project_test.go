package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadProject_SingleAgent(t *testing.T) {
	dir := t.TempDir()

	// fleet.yaml with one agent
	fleetYAML := `agents:
  - my-agent
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fleet.yaml"), []byte(fleetYAML), 0644))

	// my-agent/agent.yaml
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "my-agent"), 0755))
	agentYAML := `name: my-agent
core_version: latest
runtime:
  image: "@builtin/codex"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "my-agent", "agent.yaml"), []byte(agentYAML), 0644))

	project, err := LoadProject(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, project.Dir)
	require.Len(t, project.Agents, 1)
	assert.Equal(t, "my-agent", project.Agents[0].Name)
	assert.Equal(t, filepath.Join(dir, "my-agent"), project.Agents[0].Dir)
	assert.Equal(t, "@builtin/codex", project.Agents[0].Config.Runtime.Image)
}

func TestLoadProject_MultipleAgents(t *testing.T) {
	dir := t.TempDir()

	fleetYAML := `agents:
  - coder
  - reviewer
shared:
  installations:
    - plugin: "@builtin/github-pat"
      options:
        token: "${GITHUB_PAT}"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fleet.yaml"), []byte(fleetYAML), 0644))

	for _, name := range []string{"coder", "reviewer"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, name), 0755))
		yaml := `name: ` + name + `
core_version: latest
runtime:
  image: "@builtin/codex"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, name, "agent.yaml"), []byte(yaml), 0644))
	}

	project, err := LoadProject(dir)
	require.NoError(t, err)
	require.Len(t, project.Agents, 2)
	assert.Equal(t, "coder", project.Agents[0].Name)
	assert.Equal(t, "reviewer", project.Agents[1].Name)
	// Shared installations merged
	require.Len(t, project.Agents[0].Config.Installations, 1)
	assert.Equal(t, "@builtin/github-pat", project.Agents[0].Config.Installations[0].Plugin)
}

func TestLoadProject_NoFleetYAML(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadProject(dir)
	assert.ErrorContains(t, err, "fleet.yaml")
}
