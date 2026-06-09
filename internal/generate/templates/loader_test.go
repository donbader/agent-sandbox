package templates

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedLoader_LoadEntrypoint(t *testing.T) {
	loader := NewEmbeddedLoader()
	tmpl, err := loader.Load("entrypoint.sh.tmpl")
	require.NoError(t, err)
	assert.NotNil(t, tmpl)
}

func TestEmbeddedLoader_LoadDockerfile(t *testing.T) {
	loader := NewEmbeddedLoader()
	tmpl, err := loader.Load("Dockerfile.tmpl")
	require.NoError(t, err)
	assert.NotNil(t, tmpl)
}

func TestEmbeddedLoader_LoadRawGateway(t *testing.T) {
	loader := NewEmbeddedLoader()
	content, err := loader.LoadRaw("gateway.Dockerfile.tmpl")
	require.NoError(t, err)
	assert.Contains(t, content, "FROM alpine:")
	assert.Contains(t, content, "COPY gateway /usr/local/bin/gateway")
	assert.Contains(t, content, `CMD ["gateway"]`)
}

func TestEmbeddedLoader_NotFound(t *testing.T) {
	loader := NewEmbeddedLoader()
	_, err := loader.Load("nonexistent.tmpl")
	assert.Error(t, err)
}

func TestDirLoader(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.tmpl"), []byte("Hello {{ .Name }}"), 0644))

	loader := NewDirLoader(dir)
	tmpl, err := loader.Load("test.tmpl")
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, tmpl.Execute(&buf, map[string]string{"Name": "World"}))
	assert.Equal(t, "Hello World", buf.String())
}

func TestFindLoader_FallsBackToEmbedded(t *testing.T) {
	loader := FindLoader("")
	_, err := loader.Load("entrypoint.sh.tmpl")
	assert.NoError(t, err)
}

func TestFindLoader_UsesCoreDirTemplates(t *testing.T) {
	dir := t.TempDir()
	templatesDir := filepath.Join(dir, "templates")
	require.NoError(t, os.MkdirAll(templatesDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(templatesDir, "entrypoint.sh.tmpl"), []byte("custom"), 0644))

	loader := FindLoader(dir)
	content, err := loader.LoadRaw("entrypoint.sh.tmpl")
	require.NoError(t, err)
	assert.Equal(t, "custom", content)
}
