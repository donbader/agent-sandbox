// Package externalservices implements the external-services feature plugin.
// It connects the gateway to external services (Docker containers or HTTPS endpoints),
// optionally injecting headers into outbound requests.
package externalservices

import (
	"fmt"
	"net/url"

	"github.com/donbader/agent-sandbox/internal/resolve"
)

// ServiceConfig defines a single external service to connect.
type ServiceConfig struct {
	URL     string            `yaml:"url" schema:"Service URL: docker://host:port or https://host" required:"true"`
	Network string            `yaml:"network" schema:"External Docker network (required for docker:// URLs)"`
	Headers map[string]string `yaml:"headers" schema:"Headers to inject (values use ${VAR} references for secrets)"`
}

// Config defines the typed configuration for the external-services plugin.
type Config struct {
	Services []ServiceConfig `yaml:"services" schema:"External services to connect" required:"true"`
}

func init() {
	resolve.Register("external-services", func(_ string, cfg Config) (*resolve.FeatureContributions, error) {
		if len(cfg.Services) == 0 {
			return nil, fmt.Errorf("external-services: at least one service is required")
		}

		contrib := &resolve.FeatureContributions{
			Name: "external-services",
		}

		seenNetworks := map[string]bool{}

		for _, svc := range cfg.Services {
			if svc.URL == "" {
				return nil, fmt.Errorf("external-services: url is required")
			}

			parsed, err := url.Parse(svc.URL)
			if err != nil {
				return nil, fmt.Errorf("external-services: invalid url %q: %w", svc.URL, err)
			}

			host := parsed.Hostname()
			port := parsed.Port()

			switch parsed.Scheme {
			case "docker":
				if svc.Network == "" {
					return nil, fmt.Errorf("external-services: network is required for docker:// service %q", svc.URL)
				}
				if port == "" {
					port = "80"
				}

				// Join the Docker network
				if !seenNetworks[svc.Network] {
					seenNetworks[svc.Network] = true
					contrib.ExternalNetworks = append(contrib.ExternalNetworks, svc.Network)
				}

				// Register as HTTP service for the gateway proxy
				contrib.HTTPServices = append(contrib.HTTPServices, resolve.HTTPService{
					Host: host,
					Port: port,
				})

				// Add rewriters for headers
				if len(svc.Headers) > 0 {
					rewriters, err := buildRewriters(host, svc.Headers)
					if err != nil {
						return nil, fmt.Errorf("external-services: service %q: %w", svc.URL, err)
					}
					contrib.Rewriters = append(contrib.Rewriters, rewriters...)
				}

			case "https":
				if port == "" {
					port = "443"
				}

				// HTTPS services need MITM for header injection
				contrib.MITMDomains = append(contrib.MITMDomains, host)

				// Add rewriters for headers
				if len(svc.Headers) > 0 {
					rewriters, err := buildRewriters(host, svc.Headers)
					if err != nil {
						return nil, fmt.Errorf("external-services: service %q: %w", svc.URL, err)
					}
					contrib.Rewriters = append(contrib.Rewriters, rewriters...)
				}

			case "http":
				if port == "" {
					port = "80"
				}

				contrib.HTTPServices = append(contrib.HTTPServices, resolve.HTTPService{
					Host: host,
					Port: port,
				})

				if len(svc.Headers) > 0 {
					rewriters, err := buildRewriters(host, svc.Headers)
					if err != nil {
						return nil, fmt.Errorf("external-services: service %q: %w", svc.URL, err)
					}
					contrib.Rewriters = append(contrib.Rewriters, rewriters...)
				}

			default:
				return nil, fmt.Errorf("external-services: unsupported scheme %q in url %q (use http://, https://, or docker://)", parsed.Scheme, svc.URL)
			}
		}

		return contrib, nil
	})
}

// buildRewriters creates auth-header rewriter configs for each header entry.
func buildRewriters(host string, headers map[string]string) ([]resolve.RewriterConfig, error) {
	var rewriters []resolve.RewriterConfig
	for header, value := range headers {
		// Split value into format + env var reference.
		// e.g. "Bearer ${TOKEN}" → valueFormat="Bearer ${value}", envVar="TOKEN"
		// e.g. "${API_KEY}" → valueFormat="${value}", envVar="API_KEY"
		envVar, valueFormat, err := parseHeaderValue(value)
		if err != nil {
			return nil, fmt.Errorf("header %q: %w", header, err)
		}
		rewriters = append(rewriters, resolve.RewriterConfig{
			Type:        "auth-header",
			Domains:     []string{host},
			EnvVar:      envVar,
			Header:      header,
			ValueFormat: valueFormat,
		})
	}
	return rewriters, nil
}

// parseHeaderValue extracts the env var and format from a header value string.
// Supports:
//   - "${VAR}"           → envVar="VAR", format="${value}"
//   - "Bearer ${VAR}"   → envVar="VAR", format="Bearer ${value}"
//   - "token ${VAR}"    → envVar="VAR", format="token ${value}"
func parseHeaderValue(value string) (envVar, valueFormat string, err error) {
	// Find all ${...} references — we only support exactly one env var per header
	refs := resolve.FindEnvVarRefs(value)
	if len(refs) == 0 {
		return "", "", fmt.Errorf("value must contain a ${VAR} reference, got %q", value)
	}
	if len(refs) > 1 {
		return "", "", fmt.Errorf("only one ${VAR} reference per header is supported, got %q", value)
	}

	envVar = refs[0]
	// Replace the ${VAR} with ${value} to create the format string for the gateway
	valueFormat = resolve.ReplaceEnvVarRef(value, envVar, "${value}")
	return envVar, valueFormat, nil
}
