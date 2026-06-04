// Package sandbox provides embedded assets for the agent-sandbox CLI.
package sandbox

import "embed"

// GatewaySource contains the gateway proxy source code, SDK, and module files,
// embedded for writing to .build/gateway-src/ during generation. The Docker build
// compiles this into the gateway binary that runs inside the container.
//
//go:embed core/gateway core/sdk go.mod go.sum
var GatewaySource embed.FS

// CorePlugins contains the built-in plugin definitions (declarative YAML plugins).
//
//go:embed core/plugins
var CorePlugins embed.FS
