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
			Services: []plugin.GatewayService{
				{
					URL:     "https://github.com",
					Headers: map[string]string{"Authorization": "Bearer ghp_abc"},
				},
			},
		},
	}

	gwCfg := BuildGatewayConfig(cfg, pluginContribs)

	require.Len(t, gwCfg.Services, 2)
	assert.Equal(t, "https://api.example.com", gwCfg.Services[0].URL)
	assert.Equal(t, "https://github.com", gwCfg.Services[1].URL)
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
	// Service with no headers doesn't need MITM, so it's not in Services
	assert.Empty(t, gwCfg.Services)
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
			Services: []plugin.GatewayService{
				{URL: "https://api.telegram.org"},
			},
			Middlewares: []plugin.GatewayMiddleware{
				{Script: "./src/rewrite.ts", Domains: []string{"api.telegram.org"}},
			},
		},
	}

	gwCfg := BuildGatewayConfig(cfg, pluginContribs)

	// Middleware domains should be collected
	assert.Contains(t, gwCfg.MiddlewareDomains, "api.telegram.org")

	// Plugin service without headers should still appear in Services
	require.Len(t, gwCfg.Services, 1)
	assert.Equal(t, "https://api.telegram.org", gwCfg.Services[0].URL)
}

func TestWriteGatewayRuntimeConfig_MiddlewareDomainsMITM(t *testing.T) {
	buildDir := t.TempDir()

	gwCfg := &GatewayConfigOutput{
		Services: []GatewayServiceOutput{
			{URL: "https://api.telegram.org"},
		},
		EgressRules: []config.EgressRule{
			{Hosts: []string{"api.example.com"}, Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"}},
			{Hosts: []string{"*"}},
		},
		MiddlewareDomains: []string{"api.telegram.org"},
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
		Services: []GatewayServiceOutput{
			{URL: "https://api.telegram.org"}, // no headers — middleware handles auth
		},
		EgressRules: []config.EgressRule{
			{Hosts: []string{"*"}},
		},
	}

	err := WriteGatewayRuntimeConfig(buildDir, gwCfg)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(buildDir, "config.yaml"))
	require.NoError(t, err)

	var rc gatewayRuntimeConfig
	require.NoError(t, yaml.Unmarshal(data, &rc))

	// Plugin service domain should be MITM'd even without headers
	assert.Contains(t, rc.MITMDomains, "api.telegram.org")
}
