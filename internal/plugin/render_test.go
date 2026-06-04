package plugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderContributions(t *testing.T) {
	raw := `
name: github-pat
options:
  token:
    type: string
    required: true
contributes:
  runtime:
    extra_builds:
      - "RUN echo {{ .options.token }}"
  gateway:
    services:
      - url: https://github.com
        headers:
          Authorization: "Bearer {{ .options.token }}"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{"token": "ghp_abc123"}
	rendered, err := RenderContributions(p, opts)
	require.NoError(t, err)

	assert.Equal(t, "RUN echo ghp_abc123", rendered.Runtime.ExtraBuilds[0])
	assert.Equal(t, "Bearer ghp_abc123", rendered.Gateway.Services[0].Headers["Authorization"])
}

func TestRenderContributions_MissingRequired(t *testing.T) {
	raw := `
name: test
options:
  token:
    type: string
    required: true
contributes:
  runtime:
    extra_builds: []
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{}
	_, err = RenderContributions(p, opts)
	assert.ErrorContains(t, err, "required option \"token\" not provided")
}

func TestRenderContributions_DefaultValues(t *testing.T) {
	raw := `
name: test
options:
  version:
    type: string
    default: "1.0.0"
contributes:
  runtime:
    extra_builds:
      - "RUN install v{{ .options.version }}"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{}
	rendered, err := RenderContributions(p, opts)
	require.NoError(t, err)

	assert.Equal(t, "RUN install v1.0.0", rendered.Runtime.ExtraBuilds[0])
}
