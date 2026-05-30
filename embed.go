// Package sandbox provides embedded assets for the agent-sandbox CLI.
package sandbox

import "embed"

// GatewaySource contains the gateway proxy source code, embedded for
// writing to .build/gateway-src/ during generation. The Docker build
// compiles this into the gateway binary that runs inside the container.
//
//go:embed gateway
var GatewaySource embed.FS
