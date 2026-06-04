package plugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePluginYAML(t *testing.T) {
	raw := `
name: github-pat
options:
  token:
    type: string
    required: true
    description: "GitHub personal access token"
contributes:
  gateway:
    services:
      - url: https://github.com
        headers:
          Authorization: "Bearer {{ .options.token }}"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)
	assert.Equal(t, "github-pat", p.Name)
	assert.Contains(t, p.Options, "token")
	assert.Equal(t, "string", p.Options["token"].Type)
	assert.True(t, p.Options["token"].Required)
	assert.Len(t, p.Contributes.Gateway.Services, 1)
	assert.Equal(t, "https://github.com", p.Contributes.Gateway.Services[0].URL)
}

func TestParsePluginYAML_MissingName(t *testing.T) {
	raw := `
options:
  token:
    type: string
`
	_, err := ParsePluginYAML([]byte(raw))
	assert.ErrorContains(t, err, "name is required")
}
