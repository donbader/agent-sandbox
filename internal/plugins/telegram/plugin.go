// Package telegram implements the Telegram feature plugin.
// It provides a Telegram bot channel via the channel manager, with MITM token injection
// through the gateway.
package telegram

import (
	"github.com/donbader/agent-sandbox/internal/resolve"
)

// Config defines the typed configuration for the telegram plugin.
type Config struct {
	AccessControl AccessControl `yaml:"access_control" schema:"Access control settings for the Telegram bot" required:"true"`
	AckEmoji      *string       `yaml:"ack_emoji" schema:"Emoji reaction to acknowledge received messages" default:"👀"`
}

// AccessControl defines who can interact with the bot.
type AccessControl struct {
	AllowedUsers   []string            `yaml:"allowed_users" schema:"Telegram usernames allowed to interact" examples:"@user1,@user2" pattern:"^@"`
	RequireMention bool                `yaml:"require_mention" schema:"Whether the bot requires @mention in group chats" default:"false"`
	Groups         map[string]GroupACL `yaml:"groups" schema:"Per-group access control overrides (key: chat ID)"`
}

// GroupACL defines access control for a specific group chat.
type GroupACL struct {
	AllowedUsers   []string `yaml:"allowed_users" schema:"Users allowed in this group (overrides top-level)" examples:"@user1,@user2"`
	RequireMention *bool    `yaml:"require_mention" schema:"Override require_mention for this group"`
}

func init() {
	resolve.Register("telegram", func(_ string, cfg Config) (*resolve.FeatureContributions, error) {
		channelConfig := map[string]any{"access_control": cfg.AccessControl}
		if cfg.AckEmoji != nil {
			channelConfig["ack_emoji"] = *cfg.AckEmoji
		}
		return &resolve.FeatureContributions{
			Name:          "telegram",
			MITMDomains:   []string{"api.telegram.org"},
			ChannelName: "telegram",
			EnvVars:       []string{"TELEGRAM_BOT_TOKEN"},
			ChannelConfig:  channelConfig,
			Rewriters: []resolve.RewriterConfig{
				{
					Type:    "telegram-url",
					Domains: []string{"api.telegram.org"},
					EnvVar:  "TELEGRAM_BOT_TOKEN",
				},
			},
		}, nil
	})
}
