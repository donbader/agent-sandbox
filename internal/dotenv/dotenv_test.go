package dotenv_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/donbader/agent-sandbox/internal/dotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestLoad_BasicKeyValue(t *testing.T) {
	path := writeEnv(t, "FOO=bar\nBAZ=qux\n")
	os.Unsetenv("FOO")
	os.Unsetenv("BAZ")

	dotenv.Load(path)

	assert.Equal(t, "bar", os.Getenv("FOO"))
	assert.Equal(t, "qux", os.Getenv("BAZ"))

	os.Unsetenv("FOO")
	os.Unsetenv("BAZ")
}

func TestLoad_CommentsAndBlankLines(t *testing.T) {
	content := `
# This is a comment
KEY1=value1

# Another comment

KEY2=value2
`
	path := writeEnv(t, content)
	os.Unsetenv("KEY1")
	os.Unsetenv("KEY2")

	dotenv.Load(path)

	assert.Equal(t, "value1", os.Getenv("KEY1"))
	assert.Equal(t, "value2", os.Getenv("KEY2"))

	os.Unsetenv("KEY1")
	os.Unsetenv("KEY2")
}

func TestLoad_QuotedValues(t *testing.T) {
	content := `DOUBLE="hello world"
SINGLE='single quoted'
NOQUOTE=plain
`
	path := writeEnv(t, content)
	os.Unsetenv("DOUBLE")
	os.Unsetenv("SINGLE")
	os.Unsetenv("NOQUOTE")

	dotenv.Load(path)

	assert.Equal(t, "hello world", os.Getenv("DOUBLE"))
	assert.Equal(t, "single quoted", os.Getenv("SINGLE"))
	assert.Equal(t, "plain", os.Getenv("NOQUOTE"))

	os.Unsetenv("DOUBLE")
	os.Unsetenv("SINGLE")
	os.Unsetenv("NOQUOTE")
}

func TestLoad_ExportPrefix(t *testing.T) {
	content := "export MY_VAR=exported_value\nexport OTHER_VAR=other\n"
	path := writeEnv(t, content)
	os.Unsetenv("MY_VAR")
	os.Unsetenv("OTHER_VAR")

	dotenv.Load(path)

	assert.Equal(t, "exported_value", os.Getenv("MY_VAR"))
	assert.Equal(t, "other", os.Getenv("OTHER_VAR"))

	os.Unsetenv("MY_VAR")
	os.Unsetenv("OTHER_VAR")
}

func TestLoad_NoOverride(t *testing.T) {
	path := writeEnv(t, "EXISTING=new_value\n")
	os.Setenv("EXISTING", "original")

	dotenv.Load(path)

	assert.Equal(t, "original", os.Getenv("EXISTING"))

	os.Unsetenv("EXISTING")
}

func TestLoad_MissingFile(t *testing.T) {
	// Should not panic or error on missing file.
	dotenv.Load("/nonexistent/path/.env")
}
