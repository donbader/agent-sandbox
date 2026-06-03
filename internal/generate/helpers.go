package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/donbader/agent-sandbox/internal/resolve"
)

// parsePortMapping splits a "host:container" port string.
// If no colon, both host and container are the same port.
func parsePortMapping(port string) (hostPort, containerPort string) {
	parts := strings.SplitN(port, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return port, port
}

// copyDir recursively copies a directory.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destPath, data, info.Mode())
	})
}

func (g *Generator) hasHooks() bool {
	for _, f := range g.Features {
		if len(f.EntrypointHooks) > 0 {
			return true
		}
	}
	return false
}

func (g *Generator) hasHomeOverride() bool {
	for _, f := range g.Features {
		if f.HomeOverride != "" {
			return true
		}
	}
	return false
}

// hasMITMDomains returns true if any feature contributes MITM domains.
func (g *Generator) hasMITMDomains() bool {
	for _, f := range g.Features {
		if len(f.MITMDomains) > 0 {
			return true
		}
	}
	return false
}

// collectMITMDomains gathers all MITM domains from features.
func (g *Generator) collectMITMDomains() []string {
	var domains []string
	seen := map[string]bool{}
	for _, f := range g.Features {
		for _, d := range f.MITMDomains {
			if !seen[d] {
				seen[d] = true
				domains = append(domains, d)
			}
		}
	}
	return domains
}

func (g *Generator) collectVolumes() []string {
	var volumes []string
	for _, f := range g.Features {
		volumes = append(volumes, f.Volumes...)
	}
	return volumes
}

// collectNamedVolumes extracts volume names from "name:/path" format.
func (g *Generator) collectNamedVolumes(volumes []string) []string {
	var named []string
	seen := map[string]bool{}
	for _, v := range volumes {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) == 2 && !strings.HasPrefix(parts[0], "/") && !strings.HasPrefix(parts[0], ".") {
			if !seen[parts[0]] {
				seen[parts[0]] = true
				named = append(named, parts[0])
			}
		}
	}
	return named
}

// collectVolumePaths extracts container mount paths from "name:/path" format.
func (g *Generator) collectVolumePaths() []string {
	var paths []string
	for _, f := range g.Features {
		for _, v := range f.Volumes {
			parts := strings.SplitN(v, ":", 2)
			if len(parts) == 2 {
				paths = append(paths, parts[1])
			}
		}
	}
	return paths
}

// collectRewriters gathers all rewriter configs from features.
func (g *Generator) collectRewriters() []resolve.RewriterConfig {
	var rewriters []resolve.RewriterConfig
	for _, f := range g.Features {
		rewriters = append(rewriters, f.Rewriters...)
	}
	return rewriters
}

// collectAgentEnv gathers agent-side environment variables from features.
// These are dummy/non-secret values set in the agent container (e.g., GH_TOKEN=dummy).
func (g *Generator) collectAgentEnv() []string {
	var envs []string
	for _, f := range g.Features {
		envs = append(envs, f.AgentEnv...)
	}
	return envs
}

// logLevel returns the configured log level, defaulting to "info".
func (g *Generator) logLevel() string {
	if g.Config.LogLevel != "" {
		return g.Config.LogLevel
	}
	return "info"
}

// needsEntrypoint returns true when a custom entrypoint is needed.
// Gateway or channel-manager always requires an entrypoint.
func (g *Generator) needsEntrypoint() bool {
	if g.Gateway || g.ChannelManager {
		return true
	}
	for _, f := range g.Features {
		if len(f.EntrypointHooks) > 0 || f.HomeOverride != "" {
			return true
		}
	}
	return false
}

// copyHooks copies entrypoint hook scripts to .build/hooks/.
func (g *Generator) copyHooks() error {
	if !g.hasHooks() {
		return nil
	}

	hooksDir := filepath.Join(g.OutDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return err
	}

	for _, f := range g.Features {
		for _, hook := range f.EntrypointHooks {
			srcPath := filepath.Join(g.Dir, hook)
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return fmt.Errorf("reading hook %s: %w", hook, err)
			}
			destPath := filepath.Join(hooksDir, filepath.Base(hook))
			if err := os.WriteFile(destPath, data, 0755); err != nil {
				return err
			}
		}
	}

	return nil
}

// copyHomeOverride copies the home override directory to .build/home-override/.
func (g *Generator) copyHomeOverride() error {
	if !g.hasHomeOverride() {
		return nil
	}

	for _, f := range g.Features {
		if f.HomeOverride == "" {
			continue
		}

		srcDir := filepath.Join(g.Dir, f.HomeOverride)
		if _, err := os.Stat(srcDir); os.IsNotExist(err) {
			continue
		}

		destDir := filepath.Join(g.OutDir, "home-override")
		if err := copyDir(srcDir, destDir); err != nil {
			return fmt.Errorf("copying home override: %w", err)
		}
	}

	return nil
}
