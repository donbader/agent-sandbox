package v1

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
)

// Presets maps @builtin/* to base image + install commands.
var Presets = map[string]struct {
	BaseImage string
	Installs  []string
}{
	"@builtin/codex": {
		BaseImage: "node:24-slim",
		Installs: []string{
			"apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates && rm -rf /var/lib/apt/lists/*",
			"--mount=type=cache,target=/root/.npm npm install -g @openai/codex@0.136.0",
		},
	},
	"@builtin/claude-code": {
		BaseImage: "node:24-slim",
		Installs: []string{
			"apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates && rm -rf /var/lib/apt/lists/*",
			"--mount=type=cache,target=/root/.npm npm install -g @anthropic-ai/claude-code",
		},
	},
	"@builtin/pi": {
		BaseImage: "node:24-slim",
		Installs: []string{
			"apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates && rm -rf /var/lib/apt/lists/*",
			"--mount=type=cache,target=/root/.npm npm install -g @anthropic-ai/claude-code",
		},
	},
}

// BuildDockerfile generates a Dockerfile string from config and plugin contributions.
func BuildDockerfile(cfg *config.V1Config, contribs *plugin.Contributions) (string, error) {
	var lines []string

	// Base image
	baseImage := cfg.Runtime.Image
	var presetInstalls []string
	if preset, ok := Presets[cfg.Runtime.Image]; ok {
		baseImage = preset.BaseImage
		presetInstalls = preset.Installs
	}
	lines = append(lines, fmt.Sprintf("FROM %s", baseImage))
	lines = append(lines, "")

	// Preset installs
	for _, inst := range presetInstalls {
		lines = append(lines, fmt.Sprintf("RUN %s", inst))
	}
	if len(presetInstalls) > 0 {
		lines = append(lines, "")
	}

	// User extra builds
	lines = append(lines, cfg.Runtime.ExtraBuilds...)
	if len(cfg.Runtime.ExtraBuilds) > 0 {
		lines = append(lines, "")
	}

	// Plugin extra builds
	if contribs != nil {
		lines = append(lines, contribs.Runtime.ExtraBuilds...)
		if len(contribs.Runtime.ExtraBuilds) > 0 {
			lines = append(lines, "")
		}
	}

	// Entrypoint
	if len(cfg.Runtime.Entrypoint) > 0 {
		ep, err := json.Marshal(cfg.Runtime.Entrypoint)
		if err != nil {
			return "", fmt.Errorf("marshal entrypoint: %w", err)
		}
		lines = append(lines, fmt.Sprintf("CMD %s", string(ep)))
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n"), nil
}
