package v1

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestBuildGatewayConfig(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Services: []config.GatewayServiceEntry{
				{
					URL:     "https://api.example.com",
					Headers: map[string]string{"Authorization": "Bearer token123"},
				},
			},
		},
	}

	pluginContribs := &plugin.Contributions{
		Gateway: plugin.GatewayContrib{
			Egress: []config.EgressRule{
				{
					Hosts:   []string{"github.com"},
					Headers: map[string]string{"Authorization": "Bearer ghp_abc"},
				},
			},
		},
	}

	gwCfg := BuildGatewayConfig(cfg, pluginContribs)

	// User config produces 1 auth header (api.example.com) + plugin produces 1 (github.com)
	require.Len(t, gwCfg.AuthHeaders, 2)
	assert.Equal(t, "api.example.com", gwCfg.AuthHeaders[0].Domain)
	assert.Equal(t, "github.com", gwCfg.AuthHeaders[1].Domain)
}

func TestBuildGatewayConfig_NilContribs(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Services: []config.GatewayServiceEntry{
				{URL: "https://example.com"},
			},
		},
	}

	gwCfg := BuildGatewayConfig(cfg, nil)
	// Service with no headers doesn't produce auth headers
	assert.Empty(t, gwCfg.AuthHeaders)
	// But it's tracked in EgressRules (migrated from legacy format + catch-all)
	require.Len(t, gwCfg.EgressRules, 2) // example.com + catch-all "*"
	assert.Equal(t, []string{"example.com"}, gwCfg.EgressRules[0].Hosts)
	assert.Equal(t, []string{"*"}, gwCfg.EgressRules[1].Hosts)
}

func TestBuildGatewayConfig_MiddlewareDomains(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Egress: []config.EgressRule{
				{Hosts: []string{"*"}},
			},
		},
	}

	pluginContribs := &plugin.Contributions{
		Gateway: plugin.GatewayContrib{
			Egress: []config.EgressRule{
				{
					Hosts: []string{"api.telegram.org"},
					Middlewares: []config.MiddlewareEntry{
						{Script: "./src/rewrite.ts"},
					},
				},
			},
		},
	}

	gwCfg := BuildGatewayConfig(cfg, pluginContribs)

	// Plugin egress rule should be inserted before catch-all
	require.Len(t, gwCfg.EgressRules, 2)
	assert.Equal(t, []string{"api.telegram.org"}, gwCfg.EgressRules[0].Hosts)
	assert.Equal(t, []string{"*"}, gwCfg.EgressRules[1].Hosts)

	// Middleware on the rule means it needs MITM
	assert.True(t, gwCfg.EgressRules[0].NeedsMITM())
}

func TestWriteGatewayRuntimeConfig_MiddlewareDomainsMITM(t *testing.T) {
	buildDir := t.TempDir()

	gwCfg := &GatewayConfigOutput{
		EgressRules: []config.EgressRule{
			{Hosts: []string{"api.example.com"}, Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"}},
			{Hosts: []string{"api.telegram.org"}, Middlewares: []config.MiddlewareEntry{{Script: "./src/rewrite.ts"}}},
			{Hosts: []string{"*"}},
		},
	}

	err := WriteGatewayRuntimeConfig(buildDir, gwCfg)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(buildDir, "config.yaml"))
	require.NoError(t, err)

	var rc gatewayRuntimeConfig
	require.NoError(t, yaml.Unmarshal(data, &rc))

	// Both the egress header domain and the middleware domain should be in mitm_domains
	assert.Contains(t, rc.MITMDomains, "api.example.com")
	assert.Contains(t, rc.MITMDomains, "api.telegram.org")
}

func TestWriteGatewayRuntimeConfig_PluginServiceWithoutHeaders(t *testing.T) {
	buildDir := t.TempDir()

	gwCfg := &GatewayConfigOutput{
		EgressRules: []config.EgressRule{
			{Hosts: []string{"api.telegram.org"}, Middlewares: []config.MiddlewareEntry{{Script: "./src/rewrite.ts"}}},
			{Hosts: []string{"*"}},
		},
	}

	err := WriteGatewayRuntimeConfig(buildDir, gwCfg)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(buildDir, "config.yaml"))
	require.NoError(t, err)

	var rc gatewayRuntimeConfig
	require.NoError(t, yaml.Unmarshal(data, &rc))

	// Plugin domain with middleware should be MITM'd even without headers
	assert.Contains(t, rc.MITMDomains, "api.telegram.org")
}
