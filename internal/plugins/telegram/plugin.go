// Package telegram implements the Telegram feature plugin.
// It provides a Telegram bot channel via the bridge, with MITM token injection
// through the gateway.
package telegram

import (
	"github.com/donbader/agent-sandbox/internal/resolve"
)

// Config defines the typed configuration for the telegram plugin.
type Config struct {
	AllowedChatIDs []string `yaml:"allowed_chat_ids" schema:"Telegram chat IDs allowed to interact with this agent"`
}

func init() {
	resolve.Register("telegram", func(_ string, cfg Config) (*resolve.FeatureContributions, error) {
		return &resolve.FeatureContributions{
			MITMDomains:   []string{"api.telegram.org"},
			BridgeChannel: "telegram",
			EnvVars:       []string{"TELEGRAM_BOT_TOKEN"},
		}, nil
	})
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
