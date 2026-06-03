// Package runtime detects the container runtime available on the host.
package runtime

import (
	"fmt"
	"os/exec"
)

// Runtime identifies a container runtime engine.
type Runtime string

const (
	Docker Runtime = "docker"
	Podman Runtime = "podman"
)

// Detected holds the result of container runtime detection.
type Detected struct {
	Runtime    Runtime
	Binary     string
	ComposeCmd []string
}

// DetectWithOverride returns the detected runtime. If override is "docker" or
// "podman", that value is used directly (after verifying the binary exists on
// PATH). Otherwise it falls back to PATH auto-detection (podman preferred over
// docker). Returns an error if no runtime is found.
func DetectWithOverride(override string) (*Detected, error) {
	if override != "" {
		return resolveOverride(override)
	}
	return detectFromPath()
}

// DetectOrDefault returns the detected runtime via PATH auto-detection,
// falling back to Docker defaults if detection fails.
func DetectOrDefault() *Detected {
	return DetectOrDefaultWithOverride("")
}

// DetectOrDefaultWithOverride is like DetectOrDefault but accepts an override
// value that takes precedence over PATH detection.
func DetectOrDefaultWithOverride(override string) *Detected {
	d, err := DetectWithOverride(override)
	if err != nil {
		return &Detected{
			Runtime:    Docker,
			Binary:     "docker",
			ComposeCmd: []string{"docker", "compose"},
		}
	}
	return d
}

func resolveOverride(val string) (*Detected, error) {
	switch Runtime(val) {
	case Docker:
		if _, err := exec.LookPath("docker"); err != nil {
			return nil, fmt.Errorf("container_runtime set to %q but binary not found on PATH", val)
		}
		return buildDetected(Docker), nil
	case Podman:
		if _, err := exec.LookPath("podman"); err != nil {
			return nil, fmt.Errorf("container_runtime set to %q but binary not found on PATH", val)
		}
		return buildDetected(Podman), nil
	default:
		return nil, fmt.Errorf("unsupported container_runtime value %q: must be \"docker\" or \"podman\"", val)
	}
}

func detectFromPath() (*Detected, error) {
	if _, err := exec.LookPath("podman"); err == nil {
		return buildDetected(Podman), nil
	}
	if _, err := exec.LookPath("docker"); err == nil {
		return buildDetected(Docker), nil
	}
	return nil, fmt.Errorf("no container runtime found: install docker or podman and ensure it is on PATH")
}

func buildDetected(rt Runtime) *Detected {
	binary := string(rt)
	composeCmd := []string{binary, "compose"}

	if rt == Podman {
		if err := exec.Command("podman", "compose", "version").Run(); err != nil {
			if _, err2 := exec.LookPath("podman-compose"); err2 == nil {
				composeCmd = []string{"podman-compose"}
			}
		}
	}

	return &Detected{
		Runtime:    rt,
		Binary:     binary,
		ComposeCmd: composeCmd,
	}
}
