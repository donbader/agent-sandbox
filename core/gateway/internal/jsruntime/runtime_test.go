package jsruntime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBundle_SimpleTS(t *testing.T) {
	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "handler.ts"), []byte(`
		export default function(ctx: any): string {
			return "hello from ts";
		}
	`), 0644)
	require.NoError(t, err)

	js, err := Bundle(filepath.Join(dir, "handler.ts"))
	require.NoError(t, err)
	assert.Contains(t, js, "hello from ts")
	assert.NotContains(t, js, "ctx: any") // types stripped
}

func TestBundle_WithImports(t *testing.T) {
	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "helper.ts"), []byte(`
		export function greet(name: string): string {
			return "hello " + name;
		}
	`), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir, "handler.ts"), []byte(`
		import { greet } from "./helper";
		export default function(): string {
			return greet("world");
		}
	`), 0644)
	require.NoError(t, err)

	js, err := Bundle(filepath.Join(dir, "handler.ts"))
	require.NoError(t, err)
	assert.Contains(t, js, "hello")
	assert.Contains(t, js, "world")
}

func TestBundle_SyntaxError(t *testing.T) {
	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "bad.ts"), []byte(`
		export default function( {{{ invalid
	`), 0644)
	require.NoError(t, err)

	_, err = Bundle(filepath.Join(dir, "bad.ts"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "esbuild")
}

func TestVM_RunString(t *testing.T) {
	vm := NewVM()
	val, err := vm.RunString(`(function() { return "test_result"; })()`)
	require.NoError(t, err)
	assert.Equal(t, "test_result", val.Export())
}

func TestVM_Set(t *testing.T) {
	vm := NewVM()
	require.NoError(t, vm.Set("greeting", "hello"))

	val, err := vm.RunString(`greeting + " world"`)
	require.NoError(t, err)
	assert.Equal(t, "hello world", val.Export())
}

func TestBundle_AndExecute(t *testing.T) {
	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "add.ts"), []byte(`
		export default function(a: number, b: number): number {
			return a + b;
		}
	`), 0644)
	require.NoError(t, err)

	js, err := Bundle(filepath.Join(dir, "add.ts"))
	require.NoError(t, err)

	vm := NewVM()
	_, err = vm.RunString(js)
	require.NoError(t, err)

	// __handler.default should be the exported function
	val, err := vm.RunString(`__handler.default(3, 4)`)
	require.NoError(t, err)
	assert.Equal(t, int64(7), val.ToInteger())
}
