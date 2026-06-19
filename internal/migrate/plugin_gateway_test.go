package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectLegacyGateway_OldFormat(t *testing.T) {
	content := `
name: telegram
contributes:
  gateway:
    services:
      - url: https://api.telegram.org
    middlewares:
      - script: "./src/rewrite.ts"
        domains: ["api.telegram.org"]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	legacy, err := DetectLegacyGateway(path)
	require.NoError(t, err)
	assert.True(t, legacy)
}

func TestDetectLegacyGateway_NewFormat(t *testing.T) {
	content := `
name: github-pat
contributes:
  gateway:
    egress:
      - hosts: ["api.github.com", "github.com"]
        middlewares:
          - script: "./src/github-auth.ts"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	legacy, err := DetectLegacyGateway(path)
	require.NoError(t, err)
	assert.False(t, legacy)
}

func TestDetectLegacyGateway_NoGateway(t *testing.T) {
	content := `
name: simple-plugin
contributes:
  runtime:
    extra_builds:
      - "RUN echo hello"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	legacy, err := DetectLegacyGateway(path)
	require.NoError(t, err)
	assert.False(t, legacy)
}

func TestDetectLegacyGateway_ServicesOnly(t *testing.T) {
	content := `
name: basic
contributes:
  gateway:
    services:
      - url: https://api.example.com
`
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	legacy, err := DetectLegacyGateway(path)
	require.NoError(t, err)
	assert.True(t, legacy)
}

func TestDetectLegacyGateway_MiddlewaresOnly(t *testing.T) {
	content := `
name: mw-only
contributes:
  gateway:
    middlewares:
      - script: "./src/auth.ts"
        domains: ["api.example.com"]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	legacy, err := DetectLegacyGateway(path)
	require.NoError(t, err)
	assert.True(t, legacy)
}

func TestDetectLegacyGatewayBytes_WithTemplates(t *testing.T) {
	// This has Go templates that prevent clean YAML parse — falls back to string detection
	content := `
name: mcp-oauth
contributes:
  gateway:
    services:
{{- range $name, $cfg := .plugin.options.providers }}
      - url: "{{ index $cfg "mcp_url" }}"
{{- end }}
    middlewares:
      - script: "./src/oauth.ts"
        domains: ["{{ index $cfg "mcp_url" }}"]
`
	legacy, err := detectLegacyGatewayBytes([]byte(content))
	require.NoError(t, err)
	assert.True(t, legacy)
}

func TestTransformPlugin_ServicesOnly(t *testing.T) {
	content := `name: basic
contributes:
  gateway:
    services:
      - url: https://api.example.com
  runtime:
    extra_builds:
      - "RUN echo hello"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	before, after, err := TransformPlugin(path)
	require.NoError(t, err)
	assert.Equal(t, content, before)
	assert.Contains(t, after, "egress:")
	assert.Contains(t, after, `"api.example.com"`)
	assert.NotContains(t, after, "services:")
}

func TestTransformPlugin_ServicesAndMiddlewares(t *testing.T) {
	content := `name: telegram
contributes:
  gateway:
    services:
      - url: https://api.telegram.org
    middlewares:
      - script: "./src/telegram-token-rewrite.ts"
        domains: ["api.telegram.org"]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	_, after, err := TransformPlugin(path)
	require.NoError(t, err)
	assert.Contains(t, after, "egress:")
	assert.Contains(t, after, `"api.telegram.org"`)
	assert.Contains(t, after, "middlewares:")
	assert.Contains(t, after, `"./src/telegram-token-rewrite.ts"`)
	assert.NotContains(t, after, "services:")
	// Top-level gateway.middlewares (exactly 4-space indent) should be gone;
	// middlewares nested under egress entries (deeper indent) are expected.
	for _, line := range strings.Split(after, "\n") {
		if strings.TrimRight(line, " ") == "    middlewares:" {
			t.Error("found top-level gateway.middlewares — should be nested under egress entry")
		}
	}
}

func TestTransformPlugin_MiddlewareWithDomains(t *testing.T) {
	content := `name: multi
contributes:
  gateway:
    services:
      - url: https://api.github.com
      - url: https://api.telegram.org
    middlewares:
      - script: "./src/github-auth.ts"
        domains: ["api.github.com"]
      - script: "./src/telegram-rewrite.ts"
        domains: ["api.telegram.org"]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	_, after, err := TransformPlugin(path)
	require.NoError(t, err)
	assert.Contains(t, after, "egress:")
	assert.Contains(t, after, `"api.github.com"`)
	assert.Contains(t, after, `"api.telegram.org"`)
	assert.Contains(t, after, `"./src/github-auth.ts"`)
	assert.Contains(t, after, `"./src/telegram-rewrite.ts"`)
}

func TestTransformPlugin_PreservesOtherFields(t *testing.T) {
	content := `name: with-routes
contributes:
  gateway:
    services:
      - url: https://api.example.com
    namespaced_volumes:
      - "data:/data/plugins/test"
    routes:
      - path: "/callback"
        handler: "./src/callback.ts"
  runtime:
    extra_builds:
      - "RUN echo hello"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	_, after, err := TransformPlugin(path)
	require.NoError(t, err)
	assert.Contains(t, after, "egress:")
	assert.Contains(t, after, "namespaced_volumes:")
	assert.Contains(t, after, "routes:")
	assert.Contains(t, after, `"RUN echo hello"`)
}
