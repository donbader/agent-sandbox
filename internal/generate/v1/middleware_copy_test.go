// internal/generate/v1/middleware_copy_test.go
package v1

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyCustomMiddleware(t *testing.T) {
	projectDir := t.TempDir()
	outDir := t.TempDir()

	// Create a custom middleware file (no template)
	mwDir := filepath.Join(projectDir, "middlewares")
	require.NoError(t, os.MkdirAll(mwDir, 0755))
	mwContent := `package custom

import "github.com/donbader/agent-sandbox/core/sdk/gateway"

func init() {
    gateway.RegisterMiddleware("test", func(ctx *gateway.MiddlewareContext) error {
        return nil
    })
}
`
	require.NoError(t, os.WriteFile(filepath.Join(mwDir, "test.go"), []byte(mwContent), 0644))

	err := CopyCustomMiddleware(projectDir, outDir, []MiddlewareRef{{Path: "./middlewares/test.go", Domains: []string{"example.com"}}}, nil)
	require.NoError(t, err)

	// Verify file was copied to the custom middleware package dir
	dest := filepath.Join(outDir, "gateway-src", "core", "gateway", "middlewares", "custom", "test.go")
	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Contains(t, string(data), "RegisterMiddleware")
}

func TestCopyCustomMiddleware_TemplateRendering(t *testing.T) {
	projectDir := t.TempDir()
	outDir := t.TempDir()

	// Create a middleware template that references options
	mwDir := filepath.Join(projectDir, "middlewares")
	require.NoError(t, os.MkdirAll(mwDir, 0755))
	mwContent := `package custom

func init() {
    secret := "{{ .options.bot_token }}"
    _ = secret
}
`
	require.NoError(t, os.WriteFile(filepath.Join(mwDir, "rewrite.go"), []byte(mwContent), 0644))

	// Set env var and provide options
	t.Setenv("MY_BOT_TOKEN", "12345:ABCDEF")
	opts := map[string]any{"bot_token": "${MY_BOT_TOKEN}"}

	err := CopyCustomMiddleware(projectDir, outDir, []MiddlewareRef{{Path: "./middlewares/rewrite.go", Domains: []string{"api.telegram.org"}}}, opts)
	require.NoError(t, err)

	// Verify template was rendered with the actual secret value
	dest := filepath.Join(outDir, "gateway-src", "core", "gateway", "middlewares", "custom", "rewrite.go")
	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Contains(t, string(data), `secret := "12345:ABCDEF"`)
	assert.NotContains(t, string(data), "{{ .options")
}

func TestCopyCustomMiddleware_Empty(t *testing.T) {
	err := CopyCustomMiddleware("", "", nil, nil)
	require.NoError(t, err)
}

func TestCopyCustomMiddleware_MissingFile(t *testing.T) {
	projectDir := t.TempDir()
	outDir := t.TempDir()

	err := CopyCustomMiddleware(projectDir, outDir, []MiddlewareRef{{Path: "./nonexistent.go", Domains: []string{"example.com"}}}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read middleware")
}

func TestGenerateAuthHeaderMiddleware(t *testing.T) {
	outDir := t.TempDir()
	t.Setenv("TEST_API_KEY", "sk-secret-123")

	entries := []AuthHeaderEntry{
		{
			Domain:      "api.example.com",
			Header:      "Authorization",
			EnvVar:      "TEST_API_KEY",
			ValueFormat: "Bearer ${value}",
		},
	}

	err := GenerateAuthHeaderMiddleware(outDir, entries)
	require.NoError(t, err)

	// Verify generated file exists and contains expected code
	dest := filepath.Join(outDir, "gateway-src", "core", "gateway", "middlewares", "custom", "auth_header_api_example_com_0.go")
	data, err := os.ReadFile(dest)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "package custom")
	assert.Contains(t, content, `gateway.RegisterSecret`)
	assert.Contains(t, content, `"Bearer sk-secret-123"`)
	assert.Contains(t, content, `"api.example.com"`)
	assert.Contains(t, content, `"Authorization"`)
}

func TestGenerateAuthHeaderMiddleware_Base64Basic(t *testing.T) {
	outDir := t.TempDir()
	t.Setenv("TEST_GH_TOKEN", "ghp_abc123")

	entries := []AuthHeaderEntry{
		{
			Domain:      "github.com",
			Header:      "Authorization",
			EnvVar:      "TEST_GH_TOKEN",
			ValueFormat: "Basic ${base64_basic}",
		},
	}

	err := GenerateAuthHeaderMiddleware(outDir, entries)
	require.NoError(t, err)

	dest := filepath.Join(outDir, "gateway-src", "core", "gateway", "middlewares", "custom", "auth_header_github_com_0.go")
	data, err := os.ReadFile(dest)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "Basic ")
	assert.NotContains(t, content, "${base64_basic}")
}

func TestGenerateAuthHeaderMiddleware_SkipsMissingEnvVar(t *testing.T) {
	outDir := t.TempDir()

	entries := []AuthHeaderEntry{
		{
			Domain:      "api.example.com",
			Header:      "Authorization",
			EnvVar:      "NONEXISTENT_VAR_12345",
			ValueFormat: "Bearer ${value}",
		},
	}

	err := GenerateAuthHeaderMiddleware(outDir, entries)
	require.NoError(t, err)

	// No file should be generated for missing env var
	dest := filepath.Join(outDir, "gateway-src", "core", "gateway", "middlewares", "custom", "auth_header_api_example_com_0.go")
	_, err = os.Stat(dest)
	assert.True(t, os.IsNotExist(err))
}

func TestGenerateAuthHeaderMiddleware_Empty(t *testing.T) {
	err := GenerateAuthHeaderMiddleware("", nil)
	require.NoError(t, err)
}
