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
	BuildStages      []plugin.NamedBuildStage
	EarlyBuilds      []string // extra_builds that don't reference build stage artifacts (cacheable)
	ExtraBuilds      []string // extra_builds that reference artifacts (must come after COPY --from)
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

	// Collect build stages (user + plugin)
	var buildStages []plugin.NamedBuildStage
	for _, s := range cfg.Runtime.BuildStages {
		buildStages = append(buildStages, plugin.NamedBuildStage{
			Name:      s.Name,
			Base:      s.Base,
			Steps:     s.Steps,
			Artifacts: s.Artifacts,
		})
	}
	if contribs != nil {
		buildStages = append(buildStages, contribs.Runtime.BuildStages...)
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

	// Split extra builds: hoist steps that don't reference build stage artifacts
	// before COPY --from for better layer caching.
	artifactPaths := collectArtifactPaths(buildStages)
	earlyBuilds, lateBuilds := splitExtraBuilds(extraBuilds, artifactPaths)

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, dockerfileData{
		BaseImage:        baseImage,
		PresetInstalls:   presetInstalls,
		IsPreset:         isPreset,
		BuildStages:      buildStages,
		EarlyBuilds:      earlyBuilds,
		ExtraBuilds:      lateBuilds,
		EntrypointPath:   entrypointPath,
		GatewayRoutePath: gatewayRoutePath,
		CMD:              cmd,
	}); err != nil {
		return "", fmt.Errorf("execute Dockerfile template: %w", err)
	}
	return buf.String(), nil
}

// collectArtifactPaths returns all destination paths from build stage artifacts,
// including their parent directories (to catch steps that write to the same dir).
func collectArtifactPaths(stages []plugin.NamedBuildStage) []string {
	seen := make(map[string]bool)
	var paths []string
	for _, s := range stages {
		for _, a := range s.Artifacts {
			if !seen[a.To] {
				paths = append(paths, a.To)
				seen[a.To] = true
			}
			// Also add parent directory (e.g. /opt/telegram-adapter/dist → /opt/telegram-adapter/)
			// so steps writing to the same dir are caught.
			parent := parentDir(a.To)
			if parent != "" && !seen[parent] {
				paths = append(paths, parent)
				seen[parent] = true
			}
		}
	}
	return paths
}

// parentDir returns the parent directory of a path, or empty if too shallow.
// e.g. /opt/telegram-adapter/dist → /opt/telegram-adapter/
// e.g. /tmp/pi-acp.tgz → "" (too shallow, /tmp/ would match too broadly)
func parentDir(path string) string {
	path = strings.TrimSuffix(path, "/")
	lastSlash := strings.LastIndex(path, "/")
	if lastSlash <= 0 {
		return ""
	}
	parent := path[:lastSlash+1]
	// Skip overly broad parents like /tmp/ or /opt/
	if strings.Count(parent, "/") <= 2 {
		return ""
	}
	return parent
}

// splitExtraBuilds separates extra_builds into early (hoistable before COPY --from)
// and late (must come after). Uses a two-pass approach:
// Pass 1: COPY instructions and artifact-referencing steps go late.
// Pass 2: steps referencing paths created by late COPYs also go late.
func splitExtraBuilds(steps []string, artifactPaths []string) (early, late []string) {
	// Pass 1: initial classification
	var earlyCandidate []string
	for _, step := range steps {
		if isLateBuild(step, artifactPaths) {
			late = append(late, step)
		} else {
			earlyCandidate = append(earlyCandidate, step)
		}
	}

	// Pass 2: collect destination paths from late COPY instructions,
	// then re-check early candidates.
	var latePaths []string
	for _, step := range late {
		if dest := extractCopyDest(step); dest != "" {
			latePaths = append(latePaths, dest)
		}
	}
	if len(latePaths) == 0 {
		early = earlyCandidate
		return
	}
	for _, step := range earlyCandidate {
		if referencesAnyPath(step, latePaths) {
			late = append(late, step)
		} else {
			early = append(early, step)
		}
	}
	return
}

// extractCopyDest extracts the destination path from a COPY instruction.
func extractCopyDest(step string) string {
	trimmed := strings.TrimSpace(step)
	if !strings.HasPrefix(trimmed, "COPY ") {
		return ""
	}
	parts := strings.Fields(trimmed)
	if len(parts) < 3 {
		return ""
	}
	// Last field is destination
	dest := parts[len(parts)-1]
	dest = strings.TrimSuffix(dest, "/")
	// Return parent dir for files, dir itself for dirs
	// We return without trailing slash so Contains matches both
	// "/opt/home-seed" and "/opt/home-seed/"
	if lastSlash := strings.LastIndex(dest, "/"); lastSlash > 0 {
		// If dest looks like a dir (no extension, or was originally slash-terminated)
		// return as-is; otherwise return parent
		origDest := parts[len(parts)-1]
		if strings.HasSuffix(origDest, "/") {
			return dest // dir path without trailing slash
		}
		return dest[:lastSlash]
	}
	return dest
}

// referencesAnyPath checks if a step references any of the given paths.
func referencesAnyPath(step string, paths []string) bool {
	for _, p := range paths {
		if strings.Contains(step, p) {
			return true
		}
	}
	return false
}

// isLateBuild returns true if a Dockerfile instruction depends on build stage
// artifacts or build context (COPY), meaning it cannot be hoisted.
func isLateBuild(step string, artifactPaths []string) bool {
	// COPY instructions depend on build context (source files that change)
	trimmed := strings.TrimSpace(step)
	if strings.HasPrefix(trimmed, "COPY ") {
		return true
	}
	// Check if step references any artifact destination path
	for _, p := range artifactPaths {
		if strings.Contains(step, p) {
			return true
		}
	}
	return false
}
