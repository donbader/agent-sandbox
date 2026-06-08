package v1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/generate/templates"
	"github.com/donbader/agent-sandbox/internal/plugin"
)

// legacyPresets is a fallback for core versions that don't ship presets/ in the tarball.
// Remove once all supported core versions include presets (>= core-v0.8.0).
var legacyPresets = map[string]*Preset{
	"@builtin/codex": {
		Name:      "codex",
		BaseImage: "node:24-slim",
		Install: []string{
			"apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates iptables iputils-ping gosu && rm -rf /var/lib/apt/lists/*",
			"--mount=type=cache,target=/root/.npm npm install -g @openai/codex@0.136.0 @zed-industries/codex-acp@0.15.0",
		},
		CMD: []string{"sleep", "infinity"},
	},
	"@builtin/claude-code": {
		Name:      "claude-code",
		BaseImage: "node:24-slim",
		Install: []string{
			"apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates iptables iputils-ping gosu && rm -rf /var/lib/apt/lists/*",
			"--mount=type=cache,target=/root/.npm npm install -g @anthropic-ai/claude-code@2.1.161 @agentclientprotocol/claude-agent-acp@0.40.0",
		},
		CMD: []string{"sleep", "infinity"},
	},
	"@builtin/pi": {
		Name:      "pi",
		BaseImage: "node:24-slim",
		Install: []string{
			"apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates iptables iputils-ping gosu && rm -rf /var/lib/apt/lists/*",
			"--mount=type=cache,target=/root/.npm npm install -g @earendil-works/pi-coding-agent@0.75.5 pi-acp@0.0.27",
		},
		CMD: []string{"sleep", "infinity"},
	},
}

// entrypointData is the template data for entrypoint.sh.tmpl.
type entrypointData struct {
	PreEntrypoint string
}

// dockerfileData is the template data for Dockerfile.tmpl.
type dockerfileData struct {
	BaseImage      string
	PresetInstalls []string
	IsPreset       bool
	ExtraBuilds    []string
	EntrypointPath string
	CMD            string
}

// BuildDockerfile generates a Dockerfile string using the embedded templates.
// This is a convenience wrapper around RenderDockerfile for callers that don't manage their own Loader.
func BuildDockerfile(cfg *config.Config, contribs *plugin.Contributions, entrypointPath string, presets map[string]*Preset) (string, error) {
	return RenderDockerfile(templates.NewEmbeddedLoader(), cfg, contribs, entrypointPath, presets)
}

// EntrypointScript returns the entrypoint script using the embedded templates.
// This is a convenience wrapper around RenderEntrypointScript.
func EntrypointScript(preEntrypoint []string) string {
	s, err := RenderEntrypointScript(templates.NewEmbeddedLoader(), preEntrypoint)
	if err != nil {
		panic("entrypoint template error: " + err.Error())
	}
	return s
}

// RenderEntrypointScript executes the entrypoint template with optional pre-entrypoint commands.
func RenderEntrypointScript(loader *templates.Loader, preEntrypoint []string) (string, error) {
	tmpl, err := loader.Load("entrypoint.sh.tmpl")
	if err != nil {
		return "", fmt.Errorf("load entrypoint template: %w", err)
	}

	var preCmd string
	if len(preEntrypoint) > 0 {
		preCmd = strings.Join(preEntrypoint, "\n")
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, entrypointData{PreEntrypoint: preCmd}); err != nil {
		return "", fmt.Errorf("execute entrypoint template: %w", err)
	}
	return buf.String(), nil
}

// RenderDockerfile executes the Dockerfile template from config and plugin contributions.
func RenderDockerfile(loader *templates.Loader, cfg *config.Config, contribs *plugin.Contributions, entrypointPath string, presets map[string]*Preset) (string, error) {
	tmpl, err := loader.Load("Dockerfile.tmpl")
	if err != nil {
		return "", fmt.Errorf("load Dockerfile template: %w", err)
	}

	// Resolve preset from loaded core presets
	baseImage := cfg.Runtime.Image
	var presetInstalls []string
	var isPreset bool
	if presets != nil {
		if preset, ok := presets[cfg.Runtime.Image]; ok {
			isPreset = true
			baseImage = preset.BaseImage
			presetInstalls = preset.Install
		}
	}
	// Fallback: if preset not resolved but image looks like a builtin, use legacy defaults.
	// This handles older core versions that don't ship presets/ in the tarball.
	// Remove once all supported core versions include presets (>= core-v0.8.0).
	if !isPreset && strings.HasPrefix(cfg.Runtime.Image, "@builtin/") {
		if p, ok := legacyPresets[cfg.Runtime.Image]; ok {
			isPreset = true
			baseImage = p.BaseImage
			presetInstalls = p.Install
		}
	}

	// Collect extra builds (user + plugin)
	var extraBuilds []string
	extraBuilds = append(extraBuilds, cfg.Runtime.ExtraBuilds...)
	if contribs != nil {
		extraBuilds = append(extraBuilds, contribs.Runtime.ExtraBuilds...)
	}

	// Marshal CMD
	var cmd string
	if len(cfg.Runtime.Entrypoint) > 0 {
		ep, err := json.Marshal(cfg.Runtime.Entrypoint)
		if err != nil {
			return "", fmt.Errorf("marshal entrypoint: %w", err)
		}
		cmd = string(ep)
	} else if isPreset {
		// Use preset's cmd if defined, otherwise default to sleep infinity.
		if presets != nil {
			if p, ok := presets[cfg.Runtime.Image]; ok && len(p.CMD) > 0 {
				ep, _ := json.Marshal(p.CMD)
				cmd = string(ep)
			} else {
				cmd = `["sleep","infinity"]`
			}
		} else {
			cmd = `["sleep","infinity"]`
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, dockerfileData{
		BaseImage:      baseImage,
		PresetInstalls: presetInstalls,
		IsPreset:       isPreset,
		ExtraBuilds:    extraBuilds,
		EntrypointPath: entrypointPath,
		CMD:            cmd,
	}); err != nil {
		return "", fmt.Errorf("execute Dockerfile template: %w", err)
	}
	return buf.String(), nil
}
