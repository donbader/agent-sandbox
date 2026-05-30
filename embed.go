// Package sandbox provides embedded assets for the agent-sandbox CLI.
package sandbox

import "embed"

// GatewaySource contains the gateway proxy source code, embedded for
// writing to .build/gateway-src/ during generation. The Docker build
// compiles this into the gateway binary that runs inside the container.
//
//go:embed gateway
var GatewaySource embed.FS

// BridgeSource contains the bridge TypeScript runtime source code, embedded for
// writing to .build/bridge-src/ during generation. The Docker build
// compiles this into the bridge that runs inside the container.
//
//go:embed bridge
var BridgeSource embed.FS

// CorePlugins contains the built-in plugin definitions (runtime + core features).
// Resolution order: local ext/plugins/<name>/ → these embedded defaults.
//
//go:embed internal/plugins
var CorePlugins embed.FS
