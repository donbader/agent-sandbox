//go:build tools

package tsruntime

// Pin dependencies used by the TypeScript runtime subsystem.
// These will be imported properly once the implementation lands.
import (
	_ "github.com/dop251/goja"
	_ "github.com/evanw/esbuild/pkg/api"
)
