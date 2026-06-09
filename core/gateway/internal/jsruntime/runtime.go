// Package jsruntime provides TypeScript bundling and JavaScript execution for gateway plugins.
package jsruntime

import (
	"fmt"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
)

// Bundle takes a TypeScript entry point, resolves all imports, and returns bundled JavaScript.
func Bundle(entryPoint string) (string, error) {
	result := api.Build(api.BuildOptions{
		EntryPoints: []string{entryPoint},
		Bundle:      true,
		Write:       false,
		Format:      api.FormatIIFE,
		Platform:    api.PlatformNeutral,
		Target:      api.ES2020,
		GlobalName:  "__handler",
		Loader: map[string]api.Loader{
			".ts": api.LoaderTS,
		},
	})

	if len(result.Errors) > 0 {
		msg := result.Errors[0].Text
		if result.Errors[0].Location != nil {
			msg = fmt.Sprintf("%s:%d: %s", result.Errors[0].Location.File, result.Errors[0].Location.Line, msg)
		}
		return "", fmt.Errorf("esbuild: %s", msg)
	}

	if len(result.OutputFiles) == 0 {
		return "", fmt.Errorf("esbuild: no output")
	}

	return string(result.OutputFiles[0].Contents), nil
}

// VM wraps a goja runtime with helper methods.
type VM struct {
	runtime *goja.Runtime
}

// NewVM creates a new JavaScript VM.
func NewVM() *VM {
	rt := goja.New()
	rt.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	return &VM{runtime: rt}
}

// RunString executes JavaScript code and returns the result.
func (vm *VM) RunString(code string) (goja.Value, error) {
	return vm.runtime.RunString(code)
}

// Runtime returns the underlying goja runtime for advanced use.
func (vm *VM) Runtime() *goja.Runtime {
	return vm.runtime
}

// Set sets a global variable in the VM.
func (vm *VM) Set(name string, value any) error {
	return vm.runtime.Set(name, value)
}
