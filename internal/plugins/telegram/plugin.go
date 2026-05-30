// Package telegram implements the Telegram feature plugin.
// It provides a Telegram bot channel via the bridge, with MITM token injection
// through the gateway.
package telegram

import (
	"github.com/donbader/agent-sandbox/internal/resolve"
)

func init() {
	resolve.RegisterFeature(&Plugin{})
}

// Plugin implements resolve.FeaturePlugin for telegram.
type Plugin struct{}

func (p *Plugin) Name() string { return "telegram" }

// Resolve extracts contributions from user config in agent.yaml.
func (p *Plugin) Resolve(_ string, userConfig map[string]any) (*resolve.FeatureContributions, error) {
	contrib := &resolve.FeatureContributions{
		MITMDomains:   []string{"api.telegram.org"},
		BridgeChannel: "telegram",
		EnvVars:       []string{"TELEGRAM_BOT_TOKEN"},
	}

	// Extract allowed_chat_ids if configured
	if ids, ok := userConfig["allowed_chat_ids"]; ok {
		if arr, ok := ids.([]any); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok {
					// These are passed through to bridge config, not as env vars
					_ = s
				}
			}
		}
	}

	return contrib, nil
}

// AllowedChatIDs extracts the allowed_chat_ids from user config.
func AllowedChatIDs(userConfig map[string]any) []string {
	ids, ok := userConfig["allowed_chat_ids"]
	if !ok {
		return nil
	}
	arr, ok := ids.([]any)
	if !ok {
		return nil
	}
	var result []string
	for _, v := range arr {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
