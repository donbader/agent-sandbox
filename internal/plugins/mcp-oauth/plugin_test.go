package mcpoauth

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPOAuthPlugin_ValidConfig(t *testing.T) {
	config := map[string]any{
		"domains":    []any{"mcp.notion.com"},
		"token_file": "/data/oauth-tokens/notion.json",
	}

	plugin := resolve.RegisteredPlugins()["mcp-oauth"]
	require.NotNil(t, plugin, "mcp-oauth plugin not registered")

	contrib, err := plugin.Resolve("", config)
	require.NoError(t, err)

	assert.Equal(t, []string{"mcp.notion.com"}, contrib.MITMDomains)
	require.Len(t, contrib.Rewriters, 1)

	rw := contrib.Rewriters[0]
	assert.Equal(t, "oauth", rw.Type)
	assert.Equal(t, []string{"mcp.notion.com"}, rw.Domains)
	assert.Equal(t, "/data/oauth-tokens/notion.json", rw.TokenFile)
}

func TestMCPOAuthPlugin_MultipleDomains(t *testing.T) {
	config := map[string]any{
		"domains":    []any{"mcp.notion.com", "mcp.slack.com"},
		"token_file": "/data/oauth-tokens/multi.json",
	}

	plugin := resolve.RegisteredPlugins()["mcp-oauth"]
	require.NotNil(t, plugin, "mcp-oauth plugin not registered")

	contrib, err := plugin.Resolve("", config)
	require.NoError(t, err)

	assert.Equal(t, []string{"mcp.notion.com", "mcp.slack.com"}, contrib.MITMDomains)
	require.Len(t, contrib.Rewriters, 1)
	assert.Equal(t, []string{"mcp.notion.com", "mcp.slack.com"}, contrib.Rewriters[0].Domains)
}

func TestMCPOAuthPlugin_ErrorsWithoutDomains(t *testing.T) {
	plugin := resolve.RegisteredPlugins()["mcp-oauth"]
	require.NotNil(t, plugin, "mcp-oauth plugin not registered")

	_, err := plugin.Resolve("", map[string]any{
		"token_file": "/data/token.json",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required option 'domains'")
}

func TestMCPOAuthPlugin_ErrorsWithoutTokenFile(t *testing.T) {
	plugin := resolve.RegisteredPlugins()["mcp-oauth"]
	require.NotNil(t, plugin, "mcp-oauth plugin not registered")

	_, err := plugin.Resolve("", map[string]any{
		"domains": []any{"mcp.notion.com"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required option 'token_file'")
}
