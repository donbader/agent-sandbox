//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/generate"
	"github.com/donbader/agent-sandbox/internal/runtime"
	"github.com/donbader/agent-sandbox/plugins/codex"
	"github.com/stretchr/testify/require"
)

func TestCodexImage_Builds(t *testing.T) {
	outDir := t.TempDir()
	rt := runtime.DetectOrDefault()

	g := &generate.Generator{
		Config: &config.AgentConfig{
			Name:    "test-codex",
			Runtime: "codex",
		},
		Runtime: codex.New(),
		Dir:     t.TempDir(),
		OutDir:  outDir,
	}

	require.NoError(t, g.Run())

	// Verify container build succeeds
	cmd := exec.Command(rt.Binary, "build", "-t", "agent-sandbox-test-codex", outDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	require.NoError(t, err, "container build failed")

	// Cleanup image
	t.Cleanup(func() {
		cleanup := exec.Command(rt.Binary, "rmi", "agent-sandbox-test-codex")
		_ = cleanup.Run()
	})

	// Verify codex is installed
	out, err := exec.Command(rt.Binary, "run", "--rm", "agent-sandbox-test-codex", "codex", "--version").CombinedOutput()
	require.NoError(t, err, "codex --version failed: %s", string(out))
	t.Logf("codex version: %s", string(out))
}

func TestCodexImage_AgentUser(t *testing.T) {
	outDir := t.TempDir()
	rt := runtime.DetectOrDefault()

	g := &generate.Generator{
		Config: &config.AgentConfig{
			Name:    "test-codex-user",
			Runtime: "codex",
		},
		Runtime: codex.New(),
		Dir:     t.TempDir(),
		OutDir:  outDir,
	}

	require.NoError(t, g.Run())

	cmd := exec.Command(rt.Binary, "build", "-t", "agent-sandbox-test-user", outDir)
	require.NoError(t, cmd.Run())

	t.Cleanup(func() {
		cleanup := exec.Command(rt.Binary, "rmi", "agent-sandbox-test-user")
		_ = cleanup.Run()
	})

	// Verify runs as agent user
	out, err := exec.Command(rt.Binary, "run", "--rm", "agent-sandbox-test-user", "whoami").CombinedOutput()
	require.NoError(t, err)
	require.Contains(t, string(out), "agent")

	// Verify workdir is /home/agent
	out, err = exec.Command(rt.Binary, "run", "--rm", "agent-sandbox-test-user", "pwd").CombinedOutput()
	require.NoError(t, err)
	require.Contains(t, string(out), "/home/agent")
}

func TestGeneratedDockerfile_Valid(t *testing.T) {
	outDir := t.TempDir()

	g := &generate.Generator{
		Config: &config.AgentConfig{
			Name:    "test-valid",
			Runtime: "codex",
		},
		Runtime: codex.New(),
		Dir:     t.TempDir(),
		OutDir:  outDir,
	}

	require.NoError(t, g.Run())

	// Verify Dockerfile exists
	_, err := os.Stat(filepath.Join(outDir, "Dockerfile"))
	require.NoError(t, err)

	// Verify docker-compose.yml exists
	_, err = os.Stat(filepath.Join(outDir, "docker-compose.yml"))
	require.NoError(t, err)
}
