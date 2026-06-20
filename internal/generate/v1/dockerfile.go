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

// entrypointData is the template data for entrypoint.sh.tmpl.
type entrypointData struct {
	PreEntrypoint string
	CWD           string
}

// dockerfileData is the template data for Dockerfile.tmpl.
type dockerfileData struct {
	BaseImage        string
	PresetInstalls   []string
	IsPreset         bool
	ExtraBuilds      []string
	EntrypointPath   string
	GatewayRoutePath string
	CMD              string
}

// BuildDockerfile generates a Dockerfile string using the embedded templates.
// This is a convenience wrapper around RenderDockerfile for callers that don't manage their own Loader.
func BuildDockerfile(cfg *config.Config, contribs *plugin.Contributions, entrypointPath, gatewayRoutePath string, presets map[string]*Preset) (string, error) {
	return RenderDockerfile(templates.NewEmbeddedLoader(), cfg, contribs, entrypointPath, gatewayRoutePath, presets)
}

// EntrypointScript returns the entrypoint script using the embedded templates.
// This is a convenience wrapper around RenderEntrypointScript.
func EntrypointScript(preEntrypoint []string, cwd string) string {
	s, err := RenderEntrypointScript(templates.NewEmbeddedLoader(), preEntrypoint, cwd)
	if err != nil {
		panic("entrypoint template error: " + err.Error())
	}
	return s
}

// RenderEntrypointScript executes the entrypoint template with optional pre-entrypoint commands.
func RenderEntrypointScript(loader *templates.Loader, preEntrypoint []string, cwd string) (string, error) {
	tmpl, err := loader.Load("entrypoint.sh.tmpl")
	if err != nil {
		return "", fmt.Errorf("load entrypoint template: %w", err)
	}

	var preCmd string
	if len(preEntrypoint) > 0 {
		preCmd = strings.Join(preEntrypoint, "\n")
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, entrypointData{PreEntrypoint: preCmd, CWD: cwd}); err != nil {
		return "", fmt.Errorf("execute entrypoint template: %w", err)
	}
	return buf.String(), nil
}

// RenderDockerfile executes the Dockerfile template from config and plugin contributions.
func RenderDockerfile(loader *templates.Loader, cfg *config.Config, contribs *plugin.Contributions, entrypointPath, gatewayRoutePath string, presets map[string]*Preset) (string, error) {
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
		BaseImage:        baseImage,
		PresetInstalls:   presetInstalls,
		IsPreset:         isPreset,
		ExtraBuilds:      extraBuilds,
		EntrypointPath:   entrypointPath,
		GatewayRoutePath: gatewayRoutePath,
		CMD:              cmd,
	}); err != nil {
		return "", fmt.Errorf("execute Dockerfile template: %w", err)
	}
	return buf.String(), nil
}
