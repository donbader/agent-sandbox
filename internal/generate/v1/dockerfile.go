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

// Presets maps @builtin/* to base image + install commands.
var Presets = map[string]struct {
	BaseImage string
	Installs  []string
}{
	"@builtin/codex": {
		BaseImage: "node:24-slim",
		Installs: []string{
			"apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates iptables iputils-ping gosu && rm -rf /var/lib/apt/lists/*",
			"--mount=type=cache,target=/root/.npm npm install -g @openai/codex@0.136.0",
		},
	},
	"@builtin/claude-code": {
		BaseImage: "node:24-slim",
		Installs: []string{
			"apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates iptables iputils-ping gosu && rm -rf /var/lib/apt/lists/*",
			"--mount=type=cache,target=/root/.npm npm install -g @anthropic-ai/claude-code",
		},
	},
	"@builtin/pi": {
		BaseImage: "node:24-slim",
		Installs: []string{
			"apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates iptables iputils-ping gosu && rm -rf /var/lib/apt/lists/*",
			"--mount=type=cache,target=/root/.npm npm install -g @earendil-works/pi-coding-agent@0.75.5 pi-acp@0.0.27",
		},
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
func BuildDockerfile(cfg *config.Config, contribs *plugin.Contributions, entrypointPath string) (string, error) {
	return RenderDockerfile(templates.NewEmbeddedLoader(), cfg, contribs, entrypointPath)
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
func RenderDockerfile(loader *templates.Loader, cfg *config.Config, contribs *plugin.Contributions, entrypointPath string) (string, error) {
	tmpl, err := loader.Load("Dockerfile.tmpl")
	if err != nil {
		return "", fmt.Errorf("load Dockerfile template: %w", err)
	}

	// Resolve preset
	baseImage := cfg.Runtime.Image
	var presetInstalls []string
	_, isPreset := Presets[cfg.Runtime.Image]
	if preset, ok := Presets[cfg.Runtime.Image]; ok {
		baseImage = preset.BaseImage
		presetInstalls = preset.Installs
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
		// Presets default to sleep infinity so containers stay alive for interactive use.
		cmd = `["sleep","infinity"]`
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
