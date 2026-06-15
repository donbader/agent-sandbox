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
          Authorization: "Bearer {{ .plugin.options.token }}"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)
	assert.Equal(t, "github-pat", p.Name)
	assert.Contains(t, p.Options, "token")
	assert.Equal(t, "string", p.Options["token"].Type)
	assert.True(t, p.Options["token"].Required)
	// Contributes is raw template text — not parsed until RenderContributions
	assert.Contains(t, p.ContributesRaw, "https://github.com")
	assert.Contains(t, p.ContributesRaw, "{{ .plugin.options.token }}")
}

func TestParsePluginYAML_StructuralTemplates(t *testing.T) {
	// Tests that plugins using template directives at the YAML structure level parse correctly.
	raw := `
name: mcp-oauth
options:
  providers:
    type: object
    required: true
contributes:
  gateway:
    services:
{{- range $name, $cfg := .plugin.options.providers }}
      - url: "{{ index $cfg "mcp_url" }}"
{{- end }}
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)
	assert.Equal(t, "mcp-oauth", p.Name)
	assert.Contains(t, p.ContributesRaw, "range")
	assert.Contains(t, p.ContributesRaw, "mcp_url")
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

func TestParsePluginYAML_Functions(t *testing.T) {
	raw := `
name: deploy-version
functions:
  gitDescribe:
    script: "./scripts/git-describe.sh"
  coreVersion:
    script: "./scripts/core-version.sh"
options:
  version_key:
    type: string
    default: "VERSION"
contributes:
  runtime:
    environment:
      "{{ .plugin.options.version_key }}": "{{ call .plugin.gitDescribe }}"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)
	assert.Equal(t, "deploy-version", p.Name)
	assert.Len(t, p.Functions, 2)
	assert.Contains(t, p.Functions, "gitDescribe")
	assert.Contains(t, p.Functions, "coreVersion")
	assert.Equal(t, "./scripts/git-describe.sh", p.Functions["gitDescribe"].Script)
	assert.Equal(t, "./scripts/core-version.sh", p.Functions["coreVersion"].Script)
}

func TestParsePluginYAML_Functions_StructuralTemplate(t *testing.T) {
	// Ensures functions are parsed even when contributes uses structural templates
	// (which triggers the fallback meta-only parsing path).
	raw := `
name: dynamic-plugin
functions:
  myFunc:
    script: "./scripts/my-func.sh"
options:
  providers:
    type: object
    required: true
contributes:
  runtime:
    extra_builds:
{{- range $name, $cfg := .plugin.options.providers }}
      - "ENV {{ $name }}_VERSION={{ call .plugin.myFunc }}"
{{- end }}
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)
	assert.Equal(t, "dynamic-plugin", p.Name)
	assert.Contains(t, p.Functions, "myFunc")
	assert.Equal(t, "./scripts/my-func.sh", p.Functions["myFunc"].Script)
}
