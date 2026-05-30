// Package codex implements the codex RuntimePlugin.
package codex

import (
	"github.com/donbader/agent-sandbox/sdk"
)

// Plugin is the codex RuntimePlugin.
type Plugin struct{}

// New creates a new codex RuntimePlugin.
func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Name() string {
	return "codex"
}

func (p *Plugin) ConfigSchema() sdk.ConfigSchema {
	return sdk.ConfigSchema{}
}

func (p *Plugin) Contribute(ctx sdk.ContributeContext) (*sdk.RuntimeContributions, error) {
	return &sdk.RuntimeContributions{
		BaseImage: "node:22-slim",
		Commands: []string{
			"npm install -g @openai/codex@latest",
		},
		Cmd: []string{"sleep", "infinity"},
	}, nil
}
