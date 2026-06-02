// Package sandbox provides embedded assets for the agent-sandbox CLI.
package sandbox

import "embed"

// GatewaySource contains the gateway proxy source code, embedded for
// writing to .build/gateway-src/ during generation. The Docker build
// compiles this into the gateway binary that runs inside the container.
//
//go:embed gateway
var GatewaySource embed.FS

// ChannelManagerSource contains the channel manager TypeScript source code, embedded for
// writing to .build/channel-manager-src/ during generation. The Docker build
// compiles this into the channel manager that runs inside the container.
//
//go:embed channel-manager
var ChannelManagerSource embed.FS

// CorePlugins contains the built-in plugin definitions (runtime + core features).
// Resolution order: local ext/plugins/<name>/ → these embedded defaults.
//
//go:embed internal/plugins
var CorePlugins embed.FS
