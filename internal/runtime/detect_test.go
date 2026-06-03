package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetect_EnvDocker(t *testing.T) {
	t.Setenv("CONTAINER_RUNTIME", "docker")

	d, err := Detect()
	// Skip if docker isn't on PATH in this environment
	if err != nil {
		t.Skipf("docker not on PATH: %v", err)
	}

	assert.Equal(t, Docker, d.Runtime)
	assert.Equal(t, "docker", d.Binary)
	assert.Equal(t, []string{"docker", "compose"}, d.ComposeCmd)
}

func TestDetect_EnvPodman(t *testing.T) {
	t.Setenv("CONTAINER_RUNTIME", "podman")

	d, err := Detect()
	// Skip if podman isn't on PATH in this environment
	if err != nil {
		t.Skipf("podman not on PATH: %v", err)
	}

	assert.Equal(t, Podman, d.Runtime)
	assert.Equal(t, "podman", d.Binary)
	assert.Equal(t, []string{"podman", "compose"}, d.ComposeCmd)
}

func TestDetect_EnvInvalid(t *testing.T) {
	t.Setenv("CONTAINER_RUNTIME", "containerd")

	_, err := Detect()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported CONTAINER_RUNTIME value")
	assert.Contains(t, err.Error(), "containerd")
}

func TestDetect_FallbackFromPath(t *testing.T) {
	// Clear env to trigger PATH-based detection
	t.Setenv("CONTAINER_RUNTIME", "")

	d, err := Detect()
	if err != nil {
		t.Skipf("no container runtime on PATH: %v", err)
	}

	// Should be one of the two valid runtimes
	assert.Contains(t, []Runtime{Docker, Podman}, d.Runtime)
	assert.Equal(t, string(d.Runtime), d.Binary)
}

func TestDetect_ComposeCmdDocker(t *testing.T) {
	d := buildDetected(Docker)

	assert.Equal(t, []string{"docker", "compose"}, d.ComposeCmd)
}

func TestDetect_ComposeCmdPodman(t *testing.T) {
	d := buildDetected(Podman)

	assert.Equal(t, []string{"podman", "compose"}, d.ComposeCmd)
}

func TestDetectOrDefault_ReturnsDocker(t *testing.T) {
	// Force failure by setting an invalid runtime value
	t.Setenv("CONTAINER_RUNTIME", "nonexistent-runtime")

	d := DetectOrDefault()

	assert.Equal(t, Docker, d.Runtime)
	assert.Equal(t, "docker", d.Binary)
	assert.Equal(t, []string{"docker", "compose"}, d.ComposeCmd)
}
