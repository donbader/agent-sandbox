package externalservices

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExternalServices_DockerService(t *testing.T) {
	contrib, err := resolve.ResolveFeature(".", "external-services", "external-services", map[string]any{
		"services": []any{
			map[string]any{
				"url":     "docker://rkgw:8765",
				"network": "rkgw-external",
				"headers": map[string]any{
					"x-api-key": "${RKGW_API_KEY}",
				},
			},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"rkgw-external"}, contrib.ExternalNetworks)
	assert.Equal(t, []resolve.HTTPService{{Host: "rkgw", Port: "8765"}}, contrib.HTTPServices)

	require.Len(t, contrib.Rewriters, 1)
	rw := contrib.Rewriters[0]
	assert.Equal(t, "auth-header", rw.Type)
	assert.Equal(t, []string{"rkgw"}, rw.Domains)
	assert.Equal(t, "x-api-key", rw.Header)
	assert.Equal(t, "${value}", rw.ValueFormat)
	assert.Equal(t, "RKGW_API_KEY", rw.EnvVar)
}

func TestExternalServices_DockerDefaultPort(t *testing.T) {
	contrib, err := resolve.ResolveFeature(".", "external-services", "external-services", map[string]any{
		"services": []any{
			map[string]any{
				"url":     "docker://redis",
				"network": "my-net",
			},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, []resolve.HTTPService{{Host: "redis", Port: "80"}}, contrib.HTTPServices)
	assert.Empty(t, contrib.Rewriters)
}

func TestExternalServices_HTTPSService(t *testing.T) {
	contrib, err := resolve.ResolveFeature(".", "external-services", "external-services", map[string]any{
		"services": []any{
			map[string]any{
				"url": "https://agent-gateway.stx-ai.net",
				"headers": map[string]any{
					"Authorization": "Bearer ${STX_API_KEY}",
				},
			},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"agent-gateway.stx-ai.net"}, contrib.MITMDomains)
	assert.Empty(t, contrib.ExternalNetworks)
	assert.Empty(t, contrib.HTTPServices)

	require.Len(t, contrib.Rewriters, 1)
	rw := contrib.Rewriters[0]
	assert.Equal(t, "auth-header", rw.Type)
	assert.Equal(t, []string{"agent-gateway.stx-ai.net"}, rw.Domains)
	assert.Equal(t, "Authorization", rw.Header)
	assert.Equal(t, "Bearer ${value}", rw.ValueFormat)
	assert.Equal(t, "STX_API_KEY", rw.EnvVar)
}

func TestExternalServices_MixedServices(t *testing.T) {
	contrib, err := resolve.ResolveFeature(".", "external-services", "external-services", map[string]any{
		"services": []any{
			map[string]any{
				"url":     "docker://rkgw:8765",
				"network": "rkgw-external",
				"headers": map[string]any{"x-api-key": "${KEY}"},
			},
			map[string]any{
				"url":     "https://api.github.com",
				"headers": map[string]any{"Authorization": "Bearer ${GH_TOKEN}"},
			},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"rkgw-external"}, contrib.ExternalNetworks)
	assert.Equal(t, []string{"api.github.com"}, contrib.MITMDomains)
	assert.Equal(t, []resolve.HTTPService{{Host: "rkgw", Port: "8765"}}, contrib.HTTPServices)
	assert.Len(t, contrib.Rewriters, 2)
}

func TestExternalServices_DeduplicatesNetworks(t *testing.T) {
	contrib, err := resolve.ResolveFeature(".", "external-services", "external-services", map[string]any{
		"services": []any{
			map[string]any{"url": "docker://svc1", "network": "shared-net"},
			map[string]any{"url": "docker://svc2", "network": "shared-net"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"shared-net"}, contrib.ExternalNetworks)
}

func TestExternalServices_EmptyServicesError(t *testing.T) {
	_, err := resolve.ResolveFeature(".", "external-services", "external-services", map[string]any{
		"services": []any{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least one service")
}

func TestExternalServices_MissingURLError(t *testing.T) {
	_, err := resolve.ResolveFeature(".", "external-services", "external-services", map[string]any{
		"services": []any{
			map[string]any{"network": "some-net"},
		},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "url is required")
}

func TestExternalServices_DockerMissingNetworkError(t *testing.T) {
	_, err := resolve.ResolveFeature(".", "external-services", "external-services", map[string]any{
		"services": []any{
			map[string]any{"url": "docker://rkgw:8765"},
		},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "network is required")
}

func TestExternalServices_UnsupportedSchemeError(t *testing.T) {
	_, err := resolve.ResolveFeature(".", "external-services", "external-services", map[string]any{
		"services": []any{
			map[string]any{"url": "http://foo.com", "network": "n"},
		},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported scheme")
}

func TestExternalServices_HeaderMissingEnvVarError(t *testing.T) {
	_, err := resolve.ResolveFeature(".", "external-services", "external-services", map[string]any{
		"services": []any{
			map[string]any{
				"url":     "docker://rkgw:8765",
				"network": "net",
				"headers": map[string]any{"x-api-key": "literal-value"},
			},
		},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "${VAR} reference")
}
