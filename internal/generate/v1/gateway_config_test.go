package v1

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestBuildGatewayConfig_WithMiddleware(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Services: []config.GatewayServiceEntry{
				{
					URL: "https://api.telegram.org",
					Middlewares: []config.MiddlewareEntry{
						{Custom: "./middlewares/telegram.go"},
					},
				},
			},
		},
	}

	gwCfg := BuildGatewayConfig(cfg, nil)

	require.Len(t, gwCfg.Middlewares, 1)
	assert.Equal(t, "./middlewares/telegram.go", gwCfg.Middlewares[0].Path)
	assert.Equal(t, []string{"api.telegram.org"}, gwCfg.Middlewares[0].Domains)
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
	require.Len(t, gwCfg.Services, 1)
}
