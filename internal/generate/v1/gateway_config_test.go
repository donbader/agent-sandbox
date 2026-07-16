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
					Hosts:       []string{"api.telegram.org"},
					Middlewares: []string{"./src/rewrite.ts"},
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
			{Hosts: []string{"api.telegram.org"}, Middlewares: []string{"./src/rewrite.ts"}},
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
	// Catch-all should NOT be in mitm_domains
	assert.NotContains(t, rc.MITMDomains, "*")
}

func TestWriteGatewayRuntimeConfig_PluginServiceWithoutHeaders(t *testing.T) {
	buildDir := t.TempDir()

	gwCfg := &GatewayConfigOutput{
		EgressRules: []config.EgressRule{
			{Hosts: []string{"api.telegram.org"}, Middlewares: []string{"./src/rewrite.ts"}},
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

func TestBuildGatewayConfig_PluginEgressMerged(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Egress: []config.EgressRule{
				{Hosts: []string{"api.example.com"}, Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"}},
				{Hosts: []string{"*"}},
			},
		},
	}

	pluginContribs := &plugin.Contributions{
		Gateway: plugin.GatewayContrib{
			Egress: []config.EgressRule{
				{Hosts: []string{"api.telegram.org"}, Middlewares: []string{"./src/rewrite.ts"}},
				{Hosts: []string{"github.com"}, Headers: map[string]string{"Authorization": "Bearer ${GH_TOKEN}"}},
			},
		},
	}

	gwCfg := BuildGatewayConfig(cfg, pluginContribs)

	// Plugin rules should be inserted before catch-all
	require.Len(t, gwCfg.EgressRules, 4)
	assert.Equal(t, []string{"api.example.com"}, gwCfg.EgressRules[0].Hosts)
	assert.Equal(t, []string{"api.telegram.org"}, gwCfg.EgressRules[1].Hosts)
	assert.Equal(t, []string{"github.com"}, gwCfg.EgressRules[2].Hosts)
	assert.Equal(t, []string{"*"}, gwCfg.EgressRules[3].Hosts)

	// Middleware should be preserved on the telegram rule
	require.Len(t, gwCfg.EgressRules[1].Middlewares, 1)
	assert.Equal(t, "./src/rewrite.ts", gwCfg.EgressRules[1].Middlewares[0])

	// Auth headers should be generated for both header-based rules
	assert.Len(t, gwCfg.AuthHeaders, 2) // api.example.com + github.com
}

func TestWriteGatewayRuntimeConfig_DenyGraphQL(t *testing.T) {
	buildDir := t.TempDir()

	gwCfg := &GatewayConfigOutput{
		EgressRules: []config.EgressRule{
			{
				Hosts: []string{"api.github.com"},
				DenyGraphQL: &config.DenyGraphQL{
					Mutations: []string{"mergePullRequest", "deleteBranch"},
				},
			},
			{Hosts: []string{"*"}},
		},
	}

	err := WriteGatewayRuntimeConfig(buildDir, gwCfg)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(buildDir, "config.yaml"))
	require.NoError(t, err)

	var rc gatewayRuntimeConfig
	require.NoError(t, yaml.Unmarshal(data, &rc))

	// deny_graphql implies MITM — host should appear in mitm_domains
	assert.Contains(t, rc.MITMDomains, "api.github.com")
	assert.NotContains(t, rc.MITMDomains, "*")

	// deny_graphql config must be preserved in egress rules
	require.Len(t, rc.EgressRules, 2)
	require.NotNil(t, rc.EgressRules[0].DenyGraphQL)
	assert.Equal(t, []string{"mergePullRequest", "deleteBranch"}, rc.EgressRules[0].DenyGraphQL.Mutations)
}

func TestBuildGatewayConfig_PluginURLNormalization(t *testing.T) {
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
				{Hosts: []string{"https://mcp.notion.com/mcp"}, Middlewares: []string{"./src/oauth.ts"}},
			},
		},
	}

	gwCfg := BuildGatewayConfig(cfg, pluginContribs)

	// URL should be normalized to bare hostname
	require.Len(t, gwCfg.EgressRules, 2)
	assert.Equal(t, []string{"mcp.notion.com"}, gwCfg.EgressRules[0].Hosts)
}

func TestBuildGatewayConfig_Ingress(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Egress: []config.EgressRule{
				{Hosts: []string{"*"}},
			},
		},
	}

	pluginContribs := &plugin.Contributions{
		Gateway: plugin.GatewayContrib{
			Ingress: []plugin.IngressRule{
				{Listen: "8766", Target: "coder:8766"},
			},
		},
	}

	gwCfg := BuildGatewayConfig(cfg, pluginContribs)

	require.Len(t, gwCfg.Ingress, 1)
	assert.Equal(t, "8766", gwCfg.Ingress[0].Listen)
	assert.Equal(t, "coder:8766", gwCfg.Ingress[0].Target)
}

func TestWriteGatewayRuntimeConfig_PortForwards(t *testing.T) {
	buildDir := t.TempDir()

	gwCfg := &GatewayConfigOutput{
		EgressRules: []config.EgressRule{
			{Hosts: []string{"*"}},
		},
		Ingress: []plugin.IngressRule{
			{Listen: "8766", Target: "coder:8766"},
			{Listen: "3000", Target: "coder:3000"},
		},
	}

	err := WriteGatewayRuntimeConfig(buildDir, gwCfg)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(buildDir, "config.yaml"))
	require.NoError(t, err)

	var rc gatewayRuntimeConfig
	require.NoError(t, yaml.Unmarshal(data, &rc))

	require.Len(t, rc.PortForwards, 2)
	assert.Equal(t, ":8766", rc.PortForwards[0].Listen)
	assert.Equal(t, "coder:8766", rc.PortForwards[0].Target)
	assert.Equal(t, ":3000", rc.PortForwards[1].Listen)
	assert.Equal(t, "coder:3000", rc.PortForwards[1].Target)
}

func TestBuildGatewayConfig_UserEgressURLNormalization(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Egress: []config.EgressRule{
				{
					Hosts:   []string{"https://agw.playground.straitsx.ai/kiro/anthropic"},
					Headers: map[string]string{"Authorization": "Bearer ${STX_LLM_GATEWAY_API_KEY}"},
				},
				{Hosts: []string{"*"}},
			},
		},
	}

	gwCfg := BuildGatewayConfig(cfg, nil)

	// URL in user egress rule should be normalized to bare hostname
	require.Len(t, gwCfg.EgressRules, 2)
	assert.Equal(t, []string{"agw.playground.straitsx.ai"}, gwCfg.EgressRules[0].Hosts)
	assert.Equal(t, []string{"*"}, gwCfg.EgressRules[1].Hosts)

	// Auth header domain should also be the bare hostname, not the full URL
	require.Len(t, gwCfg.AuthHeaders, 1)
	assert.Equal(t, "agw.playground.straitsx.ai", gwCfg.AuthHeaders[0].Domain)
	assert.Equal(t, "Authorization", gwCfg.AuthHeaders[0].Header)
	assert.Equal(t, "STX_LLM_GATEWAY_API_KEY", gwCfg.AuthHeaders[0].EnvVar)
}

func TestWriteGatewayRuntimeConfig_UserURLHostInMITMDomains(t *testing.T) {
	buildDir := t.TempDir()

	// Simulate the output of BuildGatewayConfig after normalization:
	// a user egress rule with URL-style host that has headers (needs MITM).
	gwCfg := &GatewayConfigOutput{
		AuthHeaders: []AuthHeaderEntry{
			{
				Domain:      "agw.playground.straitsx.ai",
				Header:      "Authorization",
				EnvVar:      "STX_LLM_GATEWAY_API_KEY",
				ValueFormat: "Bearer ${value}",
			},
		},
		EgressRules: []config.EgressRule{
			{
				Hosts:   []string{"agw.playground.straitsx.ai"},
				Headers: map[string]string{"Authorization": "Bearer ${STX_LLM_GATEWAY_API_KEY}"},
			},
			{Hosts: []string{"*"}},
		},
	}

	err := WriteGatewayRuntimeConfig(buildDir, gwCfg)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(buildDir, "config.yaml"))
	require.NoError(t, err)

	var rc gatewayRuntimeConfig
	require.NoError(t, yaml.Unmarshal(data, &rc))

	// The normalized hostname must appear in mitm_domains
	assert.Contains(t, rc.MITMDomains, "agw.playground.straitsx.ai")

	// Auth header must use bare hostname as domain
	require.Len(t, rc.AuthHeaders, 1)
	assert.Equal(t, "agw.playground.straitsx.ai", rc.AuthHeaders[0].Domain)
	assert.Equal(t, "Authorization", rc.AuthHeaders[0].Header)
	assert.Contains(t, rc.AuthHeaders[0].Value, "${STX_LLM_GATEWAY_API_KEY}")
}

func TestBuildGatewayConfig_UserEgressMultipleURLHosts(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Egress: []config.EgressRule{
				{
					Hosts: []string{
						"https://agw.playground.straitsx.ai/kiro/anthropic",
						"https://api.openai.com/v1",
					},
					Headers: map[string]string{"Authorization": "Bearer ${API_KEY}"},
				},
				{Hosts: []string{"*"}},
			},
		},
	}

	gwCfg := BuildGatewayConfig(cfg, nil)

	// Both URL hosts should be normalized
	require.Len(t, gwCfg.EgressRules, 2)
	assert.Contains(t, gwCfg.EgressRules[0].Hosts, "agw.playground.straitsx.ai")
	assert.Contains(t, gwCfg.EgressRules[0].Hosts, "api.openai.com")

	// Auth headers should be generated for both bare hostnames
	require.Len(t, gwCfg.AuthHeaders, 2)
	domains := []string{gwCfg.AuthHeaders[0].Domain, gwCfg.AuthHeaders[1].Domain}
	assert.Contains(t, domains, "agw.playground.straitsx.ai")
	assert.Contains(t, domains, "api.openai.com")
}
