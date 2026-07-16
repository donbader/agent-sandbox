package v1

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// portOwner tracks which agent claimed a host port and from what source.
type portOwner struct {
	agent  string
	source string // e.g. "ingress listen" or "plugin mcp-oauth callback_port"
}

// validateFleet checks cross-agent configuration for conflicts and common mistakes.
// Returns an error for fatal issues (port conflicts). Prints warnings to stderr for
// non-fatal issues (unreachable OAuth callbacks, URL-style hosts).
func validateFleet(entries []ComposeAgentEntry) error {
	if err := checkPortConflicts(entries); err != nil {
		return err
	}
	checkOAuthCallbackReachability(entries)
	checkURLStyleEgressHosts(entries)
	return nil
}

// checkPortConflicts detects when multiple agents try to bind the same host port.
func checkPortConflicts(entries []ComposeAgentEntry) error {
	// Map host port → first owner
	claimed := make(map[string]portOwner)

	for _, entry := range entries {
		agentName := entry.Config.Name
		if entry.Contribs == nil {
			continue
		}

		// Ingress listen ports: the Listen field IS the host port.
		for _, ing := range entry.Contribs.Gateway.Ingress {
			port := ing.Listen
			if port == "" {
				continue
			}
			source := "ingress listen"
			if existing, conflict := claimed[port]; conflict {
				return fmt.Errorf("port conflict: host port %s is used by both %q and %q (from %s)",
					port, existing.agent, agentName, source)
			}
			claimed[port] = portOwner{agent: agentName, source: source}
		}

		// Published ports: format "host:container" — extract host port (before colon).
		for _, pp := range entry.Contribs.Gateway.PublishedPorts {
			hostPort := extractHostPort(pp)
			if hostPort == "" {
				continue
			}
			source := "plugin mcp-oauth callback_port"
			if existing, conflict := claimed[hostPort]; conflict {
				return fmt.Errorf("port conflict: host port %s is used by both %q and %q (from %s)",
					hostPort, existing.agent, agentName, source)
			}
			claimed[hostPort] = portOwner{agent: agentName, source: source}
		}
	}

	return nil
}

// checkOAuthCallbackReachability warns when an agent has a /callback route
// but no published ports to receive the redirect from the host browser.
func checkOAuthCallbackReachability(entries []ComposeAgentEntry) {
	for _, entry := range entries {
		if entry.Contribs == nil {
			continue
		}

		hasCallbackRoute := false
		for _, route := range entry.Contribs.Gateway.Routes {
			if route.Path == "/callback" {
				hasCallbackRoute = true
				break
			}
		}

		if hasCallbackRoute && len(entry.Contribs.Gateway.PublishedPorts) == 0 {
			fmt.Fprintf(os.Stderr, "warning: agent %q has OAuth callback route but no published port — OAuth redirects will be unreachable from the host browser. Set callback_port on the mcp-oauth plugin.\n",
				entry.Config.Name)
		}
	}
}

// checkURLStyleEgressHosts warns when egress hosts contain URL schemes (://),
// which will be silently normalized to bare hostnames at runtime.
func checkURLStyleEgressHosts(entries []ComposeAgentEntry) {
	for _, entry := range entries {
		for _, rule := range entry.Config.Gateway.Egress {
			for _, host := range rule.Hosts {
				if strings.Contains(host, "://") {
					normalized := normalizeURLHost(host)
					fmt.Fprintf(os.Stderr, "warning: agent %q egress host %q contains a URL — it will be normalized to %q. Use bare hostnames in egress rules.\n",
						entry.Config.Name, host, normalized)
				}
			}
		}
	}
}

// extractHostPort returns the host port from a "host:container" port mapping.
// If no colon is present, the string itself is both host and container port.
func extractHostPort(portMapping string) string {
	parts := strings.SplitN(portMapping, ":", 2)
	if len(parts) == 2 {
		return parts[0]
	}
	return portMapping
}

// normalizeURLHost extracts the hostname from a URL string.
func normalizeURLHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return parsed.Hostname()
}
