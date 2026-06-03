package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectWithOverride_Docker(t *testing.T) {
	d, err := DetectWithOverride("docker")
	if err != nil {
		t.Skipf("docker not on PATH: %v", err)
	}

	assert.Equal(t, Docker, d.Runtime)
	assert.Equal(t, "docker", d.Binary)
	assert.Equal(t, []string{"docker", "compose"}, d.ComposeCmd)
}

func TestDetectWithOverride_Podman(t *testing.T) {
	d, err := DetectWithOverride("podman")
	if err != nil {
		t.Skipf("podman not on PATH: %v", err)
	}

	assert.Equal(t, Podman, d.Runtime)
	assert.Equal(t, "podman", d.Binary)
}

func TestDetectWithOverride_InvalidReturnsError(t *testing.T) {
	_, err := DetectWithOverride("containerd")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported container_runtime value")
	assert.Contains(t, err.Error(), "containerd")
}

func TestDetectWithOverride_EmptyFallsToPath(t *testing.T) {
	d, err := DetectWithOverride("")
	if err != nil {
		t.Skipf("no container runtime on PATH: %v", err)
	}

	// Should be one of the two valid runtimes detected from PATH
	assert.Contains(t, []Runtime{Docker, Podman}, d.Runtime)
	assert.Equal(t, string(d.Runtime), d.Binary)
}

func TestDetectWithOverride_IgnoresEnvVar(t *testing.T) {
	// Env var should have no effect — only the override param matters
	t.Setenv("CONTAINER_RUNTIME", "podman")

	d, err := DetectWithOverride("docker")
	if err != nil {
		t.Skipf("docker not on PATH: %v", err)
	}

	assert.Equal(t, Docker, d.Runtime)
}

func TestDetectOrDefault_ReturnsDockerFallback(t *testing.T) {
	// DetectOrDefault calls DetectWithOverride("") which does PATH detection.
	// If PATH has no runtime, it returns Docker defaults.
	d := DetectOrDefault()

	// Should always succeed — either finds a runtime or falls back to Docker
	assert.Contains(t, []Runtime{Docker, Podman}, d.Runtime)
	assert.Equal(t, string(d.Runtime), d.Binary)
}

func TestDetectOrDefaultWithOverride_UsesOverride(t *testing.T) {
	d := DetectOrDefaultWithOverride("docker")
	// If docker isn't on PATH, it falls back to default (which is also docker)
	assert.Equal(t, Docker, d.Runtime)
	assert.Equal(t, "docker", d.Binary)
}

func TestDetectOrDefaultWithOverride_InvalidFallsToDefault(t *testing.T) {
	d := DetectOrDefaultWithOverride("bogus")

	assert.Equal(t, Docker, d.Runtime)
	assert.Equal(t, "docker", d.Binary)
	assert.Equal(t, []string{"docker", "compose"}, d.ComposeCmd)
}

func TestBuildDetected_ComposeCmdDocker(t *testing.T) {
	d := buildDetected(Docker)

	assert.Equal(t, []string{"docker", "compose"}, d.ComposeCmd)
}

func TestBuildDetected_ComposeCmdPodman(t *testing.T) {
	d := buildDetected(Podman)

	// Podman compose command depends on what's installed;
	// verify the binary field is correct regardless.
	assert.Equal(t, "podman", d.Binary)
	assert.Equal(t, Podman, d.Runtime)
}
