// Package runtime defines supported container runtime engines and their
// security-relevant properties. This is the single source of truth —
// adding a new engine here automatically updates config validation,
// socket blocking, and CLI detection.
package runtime

// Engine defines a supported container runtime and its properties.
type Engine struct {
	Name        string   // canonical name used in config ("docker", "podman")
	Binary      string   // CLI binary name
	SocketPaths []string // host socket paths to block from agent containers
}

// Supported is the canonical registry of all container runtime engines.
// Adding a new engine HERE is the ONLY step needed — config validation,
// socket blocking, and runtime detection all derive from this map.
var Supported = map[string]Engine{
	"docker": {
		Name:        "docker",
		Binary:      "docker",
		SocketPaths: []string{"/var/run/docker.sock", "/run/docker.sock"},
	},
	"podman": {
		Name:        "podman",
		Binary:      "podman",
		SocketPaths: []string{"/var/run/podman/podman.sock", "/run/podman/podman.sock"},
	},
}

// AdditionalBlockedSockets are paths blocked defensively regardless of
// the chosen engine. For example, containerd's socket is always blocked
// because Docker uses containerd internally — even on "docker" engine
// systems, the containerd socket may exist and provide runtime access.
var AdditionalBlockedSockets = []string{
	"/var/run/containerd/containerd.sock",
	"/run/containerd/containerd.sock",
}

// DangerousSocketPaths returns all socket paths that must never be
// mounted into agent containers or non-builtin sidecar containers.
func DangerousSocketPaths() []string {
	var paths []string
	for _, e := range Supported {
		paths = append(paths, e.SocketPaths...)
	}
	return append(paths, AdditionalBlockedSockets...)
}

// ValidNames returns the names of all supported runtime engines.
func ValidNames() []string {
	names := make([]string, 0, len(Supported))
	for k := range Supported {
		names = append(names, k)
	}
	return names
}

// IsValid reports whether the given engine name is supported.
func IsValid(name string) bool {
	_, ok := Supported[name]
	return ok
}
