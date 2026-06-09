package jsruntime

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostAPI_Crypto_SHA256(t *testing.T) {
	vm := NewVM()
	InjectHostAPIs(vm, &HostAPIConfig{DataDir: t.TempDir()})

	val, err := vm.RunString(`gw.crypto.sha256("hello")`)
	require.NoError(t, err)
	// SHA256 of "hello"
	assert.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", val.Export())
}

func TestHostAPI_Crypto_HMAC(t *testing.T) {
	vm := NewVM()
	InjectHostAPIs(vm, &HostAPIConfig{DataDir: t.TempDir()})

	val, err := vm.RunString(`gw.crypto.hmac("secret", "message")`)
	require.NoError(t, err)
	result, ok := val.Export().(string)
	require.True(t, ok)
	assert.Len(t, result, 64) // hex-encoded SHA256 HMAC = 64 chars
}

func TestHostAPI_Crypto_RandomBytes(t *testing.T) {
	vm := NewVM()
	InjectHostAPIs(vm, &HostAPIConfig{DataDir: t.TempDir()})

	val, err := vm.RunString(`gw.crypto.randomBytes(16).length`)
	require.NoError(t, err)
	assert.Equal(t, int64(16), val.ToInteger())
}

func TestHostAPI_Crypto_Base64URL(t *testing.T) {
	vm := NewVM()
	InjectHostAPIs(vm, &HostAPIConfig{DataDir: t.TempDir()})

	val, err := vm.RunString(`gw.crypto.base64url.encode("hello world")`)
	require.NoError(t, err)
	assert.Equal(t, "aGVsbG8gd29ybGQ", val.Export())

	val, err = vm.RunString(`gw.crypto.base64url.decode("aGVsbG8gd29ybGQ")`)
	require.NoError(t, err)
	assert.Equal(t, "hello world", val.Export())
}

func TestHostAPI_FS_ReadWrite(t *testing.T) {
	dir := t.TempDir()
	vm := NewVM()
	InjectHostAPIs(vm, &HostAPIConfig{DataDir: dir})

	// Write a file
	_, err := vm.RunString(`gw.fs.write("test.json", '{"key":"value"}')`)
	require.NoError(t, err)

	// Verify on disk
	data, err := os.ReadFile(filepath.Join(dir, "test.json"))
	require.NoError(t, err)
	assert.Equal(t, `{"key":"value"}`, string(data))

	// Read it back from JS
	val, err := vm.RunString(`gw.fs.read("test.json")`)
	require.NoError(t, err)
	assert.Equal(t, `{"key":"value"}`, val.Export())
}

func TestHostAPI_FS_Subdirectory(t *testing.T) {
	dir := t.TempDir()
	vm := NewVM()
	InjectHostAPIs(vm, &HostAPIConfig{DataDir: dir})

	_, err := vm.RunString(`gw.fs.write("subdir/deep/file.txt", "nested")`)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "subdir", "deep", "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "nested", string(data))
}

func TestHostAPI_FS_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	vm := NewVM()
	InjectHostAPIs(vm, &HostAPIConfig{DataDir: dir})

	_, err := vm.RunString(`gw.fs.read("../../etc/passwd")`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

func TestHostAPI_HTTP_Fetch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Echo-Method", r.Method)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	vm := NewVM()
	InjectHostAPIs(vm, &HostAPIConfig{DataDir: t.TempDir(), AllowPrivateIPs: true})

	val, err := vm.RunString(`
		var resp = gw.http.fetch("` + ts.URL + `", {method: "GET"});
		resp.body;
	`)
	require.NoError(t, err)
	assert.Equal(t, `{"status":"ok"}`, val.Export())
}

func TestHostAPI_HTTP_FetchWithBody(t *testing.T) {
	var receivedBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	vm := NewVM()
	InjectHostAPIs(vm, &HostAPIConfig{DataDir: t.TempDir(), AllowPrivateIPs: true})

	_, err := vm.RunString(`
		gw.http.fetch("` + ts.URL + `", {
			method: "POST",
			body: "grant_type=authorization_code&code=abc",
			headers: {"Content-Type": "application/x-www-form-urlencoded"}
		});
	`)
	require.NoError(t, err)
	assert.Equal(t, "grant_type=authorization_code&code=abc", receivedBody)
}

func TestHostAPI_Secrets(t *testing.T) {
	vm := NewVM()
	cfg := &HostAPIConfig{DataDir: t.TempDir()}
	InjectHostAPIs(vm, cfg)

	_, err := vm.RunString(`gw.secrets.register("super-secret-token")`)
	require.NoError(t, err)
	assert.Contains(t, cfg.RegisteredSecrets, "super-secret-token")
}
