// Package plugins imports all core feature plugins for their init() side effects.
// Import this package in main.go to register all built-in plugins.
package plugins

import (
	// Core feature plugins — each registers itself via init().
	_ "github.com/donbader/agent-sandbox/internal/plugins/claude-code"
	_ "github.com/donbader/agent-sandbox/internal/plugins/custom-runtime"
	_ "github.com/donbader/agent-sandbox/internal/plugins/github-pat"
	_ "github.com/donbader/agent-sandbox/internal/plugins/pi"
	_ "github.com/donbader/agent-sandbox/internal/plugins/static-header"
	_ "github.com/donbader/agent-sandbox/internal/plugins/telegram"
)
