package generate

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGatewayConfigBuilder_Render(t *testing.T) {
	t.Run("minimal config (no MITM, no rewriters, no port forwards)", func(t *testing.T) {
		gcb := &GatewayConfigBuilder{
			ListenPort: 8443,
			DNSPort:    5353,
		}

		content, err := renderTemplate("gateway-config.yaml.tmpl", gcb)
		require.NoError(t, err)

		assert.Contains(t, content, `listen: ":8443"`)
		assert.Contains(t, content, `dns_listen: ":5353"`)
		assert.NotContains(t, content, "mitm_domains:")
		assert.NotContains(t, content, "rewriters:")
		assert.NotContains(t, content, "port_forwards:")
	})

	t.Run("with MITM domains", func(t *testing.T) {
		gcb := &GatewayConfigBuilder{
			ListenPort:  8443,
			DNSPort:     5353,
			MITMDomains: []string{"api.telegram.org", "api.github.com"},
		}

		content, err := renderTemplate("gateway-config.yaml.tmpl", gcb)
		require.NoError(t, err)

		assert.Contains(t, content, "mitm_domains:")
		assert.Contains(t, content, "  - api.telegram.org")
		assert.Contains(t, content, "  - api.github.com")
	})

	t.Run("with rewriter (telegram-url type)", func(t *testing.T) {
		gcb := &GatewayConfigBuilder{
			ListenPort:  8443,
			DNSPort:     5353,
			MITMDomains: []string{"api.telegram.org"},
			Rewriters: []resolve.RewriterConfig{
				{
					Type:    "telegram-url",
					Domains: []string{"api.telegram.org"},
					EnvVar:  "TELEGRAM_BOT_TOKEN",
				},
			},
		}

		content, err := renderTemplate("gateway-config.yaml.tmpl", gcb)
		require.NoError(t, err)

		assert.Contains(t, content, "rewriters:")
		assert.Contains(t, content, "  - type: telegram-url")
		assert.Contains(t, content, "      - api.telegram.org")
		assert.Contains(t, content, "    env_var: TELEGRAM_BOT_TOKEN")
		assert.NotContains(t, content, "header:")
		assert.NotContains(t, content, "value_format:")
		assert.NotContains(t, content, "token_file:")
	})

	t.Run("with rewriter (auth-header type)", func(t *testing.T) {
		gcb := &GatewayConfigBuilder{
			ListenPort: 8443,
			DNSPort:    5353,
			Rewriters: []resolve.RewriterConfig{
				{
					Type:        "auth-header",
					Domains:     []string{"api.github.com"},
					EnvVar:      "GITHUB_TOKEN",
					Header:      "Authorization",
					ValueFormat: "token ${value}",
				},
			},
		}

		content, err := renderTemplate("gateway-config.yaml.tmpl", gcb)
		require.NoError(t, err)

		assert.Contains(t, content, "  - type: auth-header")
		assert.Contains(t, content, `    header: "Authorization"`)
		assert.Contains(t, content, `    value_format: "token ${value}"`)
		assert.Contains(t, content, "    env_var: GITHUB_TOKEN")
		assert.NotContains(t, content, "token_file:")
	})

	t.Run("with rewriter (oauth type)", func(t *testing.T) {
		gcb := &GatewayConfigBuilder{
			ListenPort: 8443,
			DNSPort:    5353,
			Rewriters: []resolve.RewriterConfig{
				{
					Type:      "oauth",
					Domains:   []string{"api.example.com"},
					TokenFile: "/secrets/oauth-token.json",
				},
			},
		}

		content, err := renderTemplate("gateway-config.yaml.tmpl", gcb)
		require.NoError(t, err)

		assert.Contains(t, content, "  - type: oauth")
		assert.Contains(t, content, `    token_file: "/secrets/oauth-token.json"`)
		assert.NotContains(t, content, "env_var:")
		assert.NotContains(t, content, "header:")
	})

	t.Run("with port forwards", func(t *testing.T) {
		gcb := &GatewayConfigBuilder{
			ListenPort: 8443,
			DNSPort:    5353,
			PortForwards: []PortForward{
				{HostPort: "1455", ContainerPort: "1455", AgentName: "coder"},
				{HostPort: "8080", ContainerPort: "3000", AgentName: "coder"},
			},
		}

		content, err := renderTemplate("gateway-config.yaml.tmpl", gcb)
		require.NoError(t, err)

		assert.Contains(t, content, "port_forwards:")
		assert.Contains(t, content, `  - listen: ":1455"`)
		assert.Contains(t, content, `    target: "coder:1455"`)
		assert.Contains(t, content, `  - listen: ":8080"`)
		assert.Contains(t, content, `    target: "coder:3000"`)
	})

	t.Run("full config matches expected format", func(t *testing.T) {
		gcb := &GatewayConfigBuilder{
			ListenPort:  8443,
			DNSPort:     5353,
			MITMDomains: []string{"api.telegram.org"},
			Rewriters: []resolve.RewriterConfig{
				{
					Type:    "telegram-url",
					Domains: []string{"api.telegram.org"},
					EnvVar:  "TELEGRAM_BOT_TOKEN",
				},
			},
			PortForwards: []PortForward{
				{HostPort: "1455", ContainerPort: "1455", AgentName: "coder"},
			},
		}

		content, err := renderTemplate("gateway-config.yaml.tmpl", gcb)
		require.NoError(t, err)

		// Verify no CA cert/key references
		assert.NotContains(t, content, "ca_cert:")
		assert.NotContains(t, content, "ca_key:")

		// Verify structure
		assert.Contains(t, content, "listen:")
		assert.Contains(t, content, "dns_listen:")
		assert.Contains(t, content, "mitm_domains:")
		assert.Contains(t, content, "rewriters:")
		assert.Contains(t, content, "port_forwards:")
	})
}

func TestBuildGatewayConfigBuilder(t *testing.T) {
	t.Run("collects MITM domains and rewriters from features", func(t *testing.T) {
		g := &Generator{
			Config:  &config.AgentConfig{Name: "coder"},
			Runtime: &resolve.RuntimeConfig{Ports: []string{"1455:1455"}},
			Features: []*resolve.FeatureContributions{
				{
					MITMDomains: []string{"api.telegram.org"},
					Rewriters: []resolve.RewriterConfig{
						{Type: "telegram-url", Domains: []string{"api.telegram.org"}, EnvVar: "TELEGRAM_BOT_TOKEN"},
					},
				},
			},
			GatewaySpec: GatewaySpec{ListenPort: 8443, HTTPListenPort: 8080, DNSPort: 5353},
		}

		gcb := g.buildGatewayConfigBuilder()

		assert.Equal(t, 8443, gcb.ListenPort)
		assert.Equal(t, 8080, gcb.HTTPListenPort)
		assert.Equal(t, 5353, gcb.DNSPort)
		assert.Equal(t, []string{"api.telegram.org"}, gcb.MITMDomains)
		assert.Len(t, gcb.Rewriters, 1)
		assert.Equal(t, "telegram-url", gcb.Rewriters[0].Type)
		assert.Len(t, gcb.PortForwards, 1)
		assert.Equal(t, "1455", gcb.PortForwards[0].HostPort)
		assert.Equal(t, "1455", gcb.PortForwards[0].ContainerPort)
		assert.Equal(t, "coder", gcb.PortForwards[0].AgentName)
	})
}

func TestSplitDomainsByScheme(t *testing.T) {
	t.Run("no scheme defaults to MITM", func(t *testing.T) {
		g := &Generator{
			Features: []*resolve.FeatureContributions{
				{MITMDomains: []string{"api.github.com", "api.telegram.org"}},
			},
		}

		mitm, httpDomains := g.splitDomainsByScheme()

		assert.Equal(t, []string{"api.github.com", "api.telegram.org"}, mitm)
		assert.Empty(t, httpDomains)
	})

	t.Run("http:// scheme goes to HTTP domains", func(t *testing.T) {
		g := &Generator{
			Features: []*resolve.FeatureContributions{
				{MITMDomains: []string{"http://host.containers.internal:8000"}},
			},
		}

		mitm, httpDomains := g.splitDomainsByScheme()

		assert.Empty(t, mitm)
		assert.Equal(t, []string{"host.containers.internal:8000"}, httpDomains)
	})

	t.Run("https:// scheme goes to MITM domains (stripped)", func(t *testing.T) {
		g := &Generator{
			Features: []*resolve.FeatureContributions{
				{MITMDomains: []string{"https://api.github.com"}},
			},
		}

		mitm, httpDomains := g.splitDomainsByScheme()

		assert.Equal(t, []string{"api.github.com"}, mitm)
		assert.Empty(t, httpDomains)
	})

	t.Run("mixed schemes are separated correctly", func(t *testing.T) {
		g := &Generator{
			Features: []*resolve.FeatureContributions{
				{MITMDomains: []string{
					"api.github.com",
					"http://host.containers.internal:8000",
					"https://api.telegram.org",
					"http://host.containers.internal:9000",
				}},
			},
		}

		mitm, httpDomains := g.splitDomainsByScheme()

		assert.Equal(t, []string{"api.github.com", "api.telegram.org"}, mitm)
		assert.Equal(t, []string{"host.containers.internal:8000", "host.containers.internal:9000"}, httpDomains)
	})
}

func TestCollectHTTPPorts(t *testing.T) {
	t.Run("extracts ports from HTTP domains", func(t *testing.T) {
		g := &Generator{
			Features: []*resolve.FeatureContributions{
				{MITMDomains: []string{
					"http://host.containers.internal:8000",
					"http://host.containers.internal:9000",
				}},
			},
		}

		ports := g.collectHTTPPorts()

		assert.Equal(t, []string{"8000", "9000"}, ports)
	})

	t.Run("defaults to port 80 when no port in URL", func(t *testing.T) {
		g := &Generator{
			Features: []*resolve.FeatureContributions{
				{MITMDomains: []string{"http://example.com"}},
			},
		}

		ports := g.collectHTTPPorts()

		assert.Equal(t, []string{"80"}, ports)
	})

	t.Run("deduplicates ports", func(t *testing.T) {
		g := &Generator{
			Features: []*resolve.FeatureContributions{
				{MITMDomains: []string{
					"http://host1.internal:8000",
					"http://host2.internal:8000",
				}},
			},
		}

		ports := g.collectHTTPPorts()

		assert.Equal(t, []string{"8000"}, ports)
	})

	t.Run("no HTTP domains returns empty", func(t *testing.T) {
		g := &Generator{
			Features: []*resolve.FeatureContributions{
				{MITMDomains: []string{"api.github.com"}},
			},
		}

		ports := g.collectHTTPPorts()

		assert.Empty(t, ports)
	})
}

func TestGatewayConfigBuilder_HTTPDomains(t *testing.T) {
	t.Run("renders HTTP domains in config", func(t *testing.T) {
		gcb := &GatewayConfigBuilder{
			ListenPort:     8443,
			HTTPListenPort: 8080,
			DNSPort:        5353,
			HTTPDomains:    []string{"host.containers.internal:8000"},
		}

		content, err := renderTemplate("gateway-config.yaml.tmpl", gcb)
		require.NoError(t, err)

		assert.Contains(t, content, `http_listen: ":8080"`)
		assert.Contains(t, content, "http_domains:")
		assert.Contains(t, content, "  - host.containers.internal:8000")
	})

	t.Run("omits http_domains when empty", func(t *testing.T) {
		gcb := &GatewayConfigBuilder{
			ListenPort:     8443,
			HTTPListenPort: 8080,
			DNSPort:        5353,
		}

		content, err := renderTemplate("gateway-config.yaml.tmpl", gcb)
		require.NoError(t, err)

		assert.NotContains(t, content, "http_domains:")
	})
}
