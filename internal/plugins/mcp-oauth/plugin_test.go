package mcpoauth

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPOAuthPlugin_ValidConfig(t *testing.T) {
	config := map[string]any{
		"providers": map[string]any{
			"notion": map[string]any{
				"mcp_url": "https://mcp.notion.com/mcp",
			},
		},
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

	// Should contribute oauth config to channel-manager
	assert.NotNil(t, contrib.ChannelConfig["oauth"])

	// Should contribute a volume for token storage
	assert.Contains(t, contrib.Volumes, "oauth-tokens:/data/oauth-tokens")

	// Should set command plugin dir
	assert.Equal(t, "command", contrib.CommandPluginDir)
}

func TestMCPOAuthPlugin_MultipleProviders(t *testing.T) {
	config := map[string]any{
		"providers": map[string]any{
			"notion": map[string]any{
				"mcp_url": "https://mcp.notion.com/mcp",
			},
			"slack": map[string]any{
				"mcp_url":       "https://mcp.slack.com/mcp",
				"client_id":     "slack-client-id",
				"client_secret": "slack-secret",
			},
		},
	}

	plugin := resolve.RegisteredPlugins()["mcp-oauth"]
	require.NotNil(t, plugin, "mcp-oauth plugin not registered")

	contrib, err := plugin.Resolve("", config)
	require.NoError(t, err)

	assert.Len(t, contrib.MITMDomains, 2)
	assert.Contains(t, contrib.MITMDomains, "mcp.notion.com")
	assert.Contains(t, contrib.MITMDomains, "mcp.slack.com")
	assert.Len(t, contrib.Rewriters, 2)

	// OAuth config should include both providers
	oauthConfig := contrib.ChannelConfig["oauth"].(map[string]any)
	providers := oauthConfig["providers"].(map[string]any)
	assert.Contains(t, providers, "notion")
	assert.Contains(t, providers, "slack")

	// Slack should have client_id
	slackCfg := providers["slack"].(map[string]any)
	assert.Equal(t, "slack-client-id", slackCfg["client_id"])
}

func TestMCPOAuthPlugin_CustomTokenDir(t *testing.T) {
	config := map[string]any{
		"providers": map[string]any{
			"notion": map[string]any{
				"mcp_url": "https://mcp.notion.com/mcp",
			},
		},
		"token_dir": "/custom/tokens",
	}

	plugin := resolve.RegisteredPlugins()["mcp-oauth"]
	require.NotNil(t, plugin, "mcp-oauth plugin not registered")

	contrib, err := plugin.Resolve("", config)
	require.NoError(t, err)

	assert.Equal(t, "/custom/tokens/notion.json", contrib.Rewriters[0].TokenFile)
	assert.Contains(t, contrib.Volumes, "oauth-tokens:/custom/tokens")
}

func TestMCPOAuthPlugin_ErrorsWithoutProviders(t *testing.T) {
	plugin := resolve.RegisteredPlugins()["mcp-oauth"]
	require.NotNil(t, plugin, "mcp-oauth plugin not registered")

	_, err := plugin.Resolve("", map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one provider")
}

func TestMCPOAuthPlugin_ErrorsWithoutMCPURL(t *testing.T) {
	plugin := resolve.RegisteredPlugins()["mcp-oauth"]
	require.NotNil(t, plugin, "mcp-oauth plugin not registered")

	_, err := plugin.Resolve("", map[string]any{
		"providers": map[string]any{
			"notion": map[string]any{},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required 'mcp_url'")
}
