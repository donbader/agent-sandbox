package generate

// GatewaySpec defines how the gateway container is built and configured.
// Injected into Generator by the CLI — generator doesn't own these details.
type GatewaySpec struct {
	BuildImage     string // Docker image for compilation (e.g. "golang:1.26.4-alpine")
	BinaryPath     string // output binary path (e.g. "/gateway")
	ListenPort     int    // TLS interception port (e.g. 8443)
	HTTPListenPort int    // HTTP proxy port (e.g. 8080)
	DNSPort        int    // DNS resolver port (e.g. 5353)
}

// ChannelManagerSpec defines how the channel manager is built and configured.
// Injected into Generator by the CLI — generator doesn't own these details.
type ChannelManagerSpec struct {
	BuildImage string // Docker image for compilation (e.g. "node:22-slim")
	InstallCmd string // dependency install command (e.g. "npm install")
	BuildCmd   string // compilation command (e.g. "npm run build")
	DistDir    string // compiled output directory (e.g. "/src/dist")
	EntryPoint string // runtime entry point (e.g. "node /opt/channel-manager/dist/index.js")
}
