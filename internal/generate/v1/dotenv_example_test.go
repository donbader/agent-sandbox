package v1

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteEnvExample_CollectsFromEgressHeaders(t *testing.T) {
	projectDir := t.TempDir()

	entries := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Gateway: config.GatewayConfig{
					Egress: []config.EgressRule{
						{
							Hosts:   []string{"api.example.com"},
							Headers: map[string]string{"Authorization": "Bearer ${API_TOKEN}"},
						},
					},
				},
			},
			Contribs: &plugin.Contributions{},
		},
	}

	err := WriteEnvExample(projectDir, entries)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(projectDir, ".env.example"))
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "API_TOKEN=")
}

func TestWriteEnvExample_CollectsFromPluginOptions(t *testing.T) {
	projectDir := t.TempDir()

	entries := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Installations: []config.Installation{
					{
						Plugin: "@builtin/github-pat",
						Options: map[string]any{
							"token": "${GITHUB_PAT}",
						},
					},
					{
						Plugin: "@builtin/telegram",
						Options: map[string]any{
							"bot_token": "${TELEGRAM_BOT_TOKEN}",
						},
					},
				},
			},
			Contribs: &plugin.Contributions{},
		},
	}

	err := WriteEnvExample(projectDir, entries)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(projectDir, ".env.example"))
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "GITHUB_PAT=")
	assert.Contains(t, content, "TELEGRAM_BOT_TOKEN=")
}

func TestWriteEnvExample_CollectsFromMultipleAgents(t *testing.T) {
	projectDir := t.TempDir()

	entries := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Gateway: config.GatewayConfig{
					Egress: []config.EgressRule{
						{Hosts: []string{"api.example.com"}, Headers: map[string]string{"X-Key": "${KEY_A}"}},
					},
				},
				Installations: []config.Installation{
					{Plugin: "foo", Options: map[string]any{"token": "${TOKEN_A}"}},
				},
			},
			Contribs: &plugin.Contributions{},
		},
		{
			Config: &config.Config{
				Installations: []config.Installation{
					{Plugin: "bar", Options: map[string]any{"secret": "${SECRET_B}"}},
				},
			},
			Contribs: &plugin.Contributions{},
		},
	}

	err := WriteEnvExample(projectDir, entries)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(projectDir, ".env.example"))
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "KEY_A=")
	assert.Contains(t, content, "TOKEN_A=")
	assert.Contains(t, content, "SECRET_B=")
}

func TestWriteEnvExample_SortedAlphabetically(t *testing.T) {
	projectDir := t.TempDir()

	entries := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Installations: []config.Installation{
					{Plugin: "a", Options: map[string]any{
						"z": "${ZEBRA}",
						"a": "${ALPHA}",
						"m": "${MANGO}",
					}},
				},
			},
			Contribs: &plugin.Contributions{},
		},
	}

	err := WriteEnvExample(projectDir, entries)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(projectDir, ".env.example"))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// Skip comment header lines
	var varLines []string
	for _, line := range lines {
		if !strings.HasPrefix(line, "#") && line != "" {
			varLines = append(varLines, line)
		}
	}

	require.Len(t, varLines, 3)
	assert.Equal(t, "ALPHA=", varLines[0])
	assert.Equal(t, "MANGO=", varLines[1])
	assert.Equal(t, "ZEBRA=", varLines[2])
}

func TestWriteEnvExample_DeduplicatesAcrossAgents(t *testing.T) {
	projectDir := t.TempDir()

	entries := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Installations: []config.Installation{
					{Plugin: "a", Options: map[string]any{"token": "${SHARED_TOKEN}"}},
				},
			},
			Contribs: &plugin.Contributions{},
		},
		{
			Config: &config.Config{
				Installations: []config.Installation{
					{Plugin: "b", Options: map[string]any{"token": "${SHARED_TOKEN}"}},
				},
			},
			Contribs: &plugin.Contributions{},
		},
	}

	err := WriteEnvExample(projectDir, entries)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(projectDir, ".env.example"))
	require.NoError(t, err)

	content := string(data)
	// Should appear exactly once
	assert.Equal(t, 1, strings.Count(content, "SHARED_TOKEN="))
}

func TestWriteEnvExample_NoFileWhenNoVars(t *testing.T) {
	projectDir := t.TempDir()

	entries := []ComposeAgentEntry{
		{
			Config:   &config.Config{},
			Contribs: &plugin.Contributions{},
		},
	}

	err := WriteEnvExample(projectDir, entries)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(projectDir, ".env.example"))
	assert.True(t, os.IsNotExist(err), "should not create .env.example when no env vars are found")
}

func TestWriteEnvExample_CollectsFromPluginContribHeaders(t *testing.T) {
	projectDir := t.TempDir()

	entries := []ComposeAgentEntry{
		{
			Config: &config.Config{},
			Contribs: &plugin.Contributions{
				Gateway: plugin.GatewayContrib{
					Egress: []config.EgressRule{
						{
							Hosts:   []string{"github.com"},
							Headers: map[string]string{"Authorization": "Bearer ${GH_TOKEN}"},
						},
					},
				},
			},
		},
	}

	err := WriteEnvExample(projectDir, entries)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(projectDir, ".env.example"))
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "GH_TOKEN=")
}

func TestWriteEnvExample_NestedOptions(t *testing.T) {
	projectDir := t.TempDir()

	entries := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Installations: []config.Installation{
					{
						Plugin: "complex",
						Options: map[string]any{
							"nested": map[string]any{
								"deep_key": "${DEEP_SECRET}",
							},
							"list": []any{"${LIST_TOKEN}", "static"},
						},
					},
				},
			},
			Contribs: &plugin.Contributions{},
		},
	}

	err := WriteEnvExample(projectDir, entries)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(projectDir, ".env.example"))
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "DEEP_SECRET=")
	assert.Contains(t, content, "LIST_TOKEN=")
}
