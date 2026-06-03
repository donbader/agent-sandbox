package generate

import (
	"fmt"
	"net"
	"net/url"
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

func (g *Generator) hasRootHooks() bool {
	for _, f := range g.Features {
		if len(f.RootHooks) > 0 {
			return true
		}
	}
	return false
}

// collectCapabilities gathers all capabilities from features, deduped.
func (g *Generator) collectCapabilities() []string {
	var caps []string
	seen := map[string]bool{}
	for _, f := range g.Features {
		for _, c := range f.Capabilities {
			if !seen[c] {
				seen[c] = true
				caps = append(caps, c)
			}
		}
	}
	return caps
}

// collectFeaturePorts gathers all port mappings from features.
func (g *Generator) collectFeaturePorts() []string {
	var ports []string
	for _, f := range g.Features {
		ports = append(ports, f.Ports...)
	}
	return ports
}

func (g *Generator) hasHomeOverride() bool {
	for _, f := range g.Features {
		if f.HomeOverride != "" {
			return true
		}
	}
	return false
}

// hasMITMDomains returns true if any feature contributes domains that require
// TLS interception (https:// or bare domains without scheme). HTTP-only domains
// are handled by the HTTP proxy and do not need MITM.
func (g *Generator) hasMITMDomains() bool {
	mitmDomains, _ := g.splitDomainsByScheme()
	return len(mitmDomains) > 0
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
// Domains are normalized to strip the scheme but preserve host:port so that
// the gateway's AuthHeaderRewriter can do port-aware matching (two services
// on the same host with different ports get distinct rewriters).
func (g *Generator) collectRewriters() []resolve.RewriterConfig {
	var rewriters []resolve.RewriterConfig
	for _, f := range g.Features {
		for _, rw := range f.Rewriters {
			normalized := make([]string, 0, len(rw.Domains))
			for _, d := range rw.Domains {
				normalized = append(normalized, stripScheme(d))
			}
			rw.Domains = normalized
			rewriters = append(rewriters, rw)
		}
	}
	return rewriters
}

// collectExternalNetworks gathers deduplicated external network names from features.
func (g *Generator) collectExternalNetworks() []string {
	var networks []string
	seen := map[string]bool{}
	for _, f := range g.Features {
		for _, n := range f.ExternalNetworks {
			if !seen[n] {
				seen[n] = true
				networks = append(networks, n)
			}
		}
	}
	return networks
}

// collectHTTPServices gathers HTTP service targets from features.
func (g *Generator) collectHTTPServices() []resolve.HTTPService {
	var services []resolve.HTTPService
	for _, f := range g.Features {
		services = append(services, f.HTTPServices...)
	}
	return services
}

// collectHTTPPorts gathers deduplicated ports from HTTP services for iptables rules.
func (g *Generator) collectHTTPPorts() []string {
	var ports []string
	seen := map[string]bool{}
	for _, f := range g.Features {
		for _, svc := range f.HTTPServices {
			port := svc.Port
			if port == "" {
				port = "80"
			}
			if !seen[port] {
				seen[port] = true
				ports = append(ports, port)
			}
		}
	}
	return ports
}

// stripScheme removes the URL scheme from a domain string but preserves the
// host:port so that port-aware matching works correctly in the gateway.
// e.g. "http://host.internal:8000" -> "host.internal:8000"
// e.g. "https://api.github.com" -> "api.github.com"
func stripScheme(d string) string {
	if strings.Contains(d, "://") {
		parsed, err := url.Parse(d)
		if err == nil {
			return parsed.Host
		}
	}
	return d
}

// stripSchemeAndPort extracts the bare hostname from a domain string that may
// include a scheme and/or port (e.g. "http://host.internal:8000" -> "host.internal").
func stripSchemeAndPort(d string) string {
	d = stripScheme(d)
	if h, _, err := net.SplitHostPort(d); err == nil {
		return h
	}
	return d
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
		if len(f.EntrypointHooks) > 0 || len(f.RootHooks) > 0 || f.HomeOverride != "" {
			return true
		}
	}
	return false
}

// copyHooks copies entrypoint hook scripts and root hook scripts to .build/hooks/.
func (g *Generator) copyHooks() error {
	if !g.hasHooks() && !g.hasRootHooks() {
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
		for _, hook := range f.RootHooks {
			srcPath := filepath.Join(g.Dir, hook)
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return fmt.Errorf("reading root hook %s: %w", hook, err)
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
