package v1

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testPresets provides preset data for tests without needing a core directory.
var testPresets = map[string]*Preset{
	"@builtin/codex": {
		Name:      "codex",
		BaseImage: "node:24-slim",
		Install: []string{
			"apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates iptables iputils-ping gosu && rm -rf /var/lib/apt/lists/*",
			"--mount=type=cache,target=/root/.npm npm install -g @openai/codex@0.136.0 @zed-industries/codex-acp@0.15.0",
		},
		CMD: []string{"sleep", "infinity"},
	},
	"@builtin/pi": {
		Name:      "pi",
		BaseImage: "node:24-slim",
		Install: []string{
			"apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates iptables iputils-ping gosu && rm -rf /var/lib/apt/lists/*",
			"--mount=type=cache,target=/root/.npm npm install -g @earendil-works/pi-coding-agent@0.75.5 pi-acp@0.0.27",
		},
		CMD: []string{"sleep", "infinity"},
	},
}

func TestBuildDockerfile(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Image:       "node:24-slim",
			ExtraBuilds: []string{"RUN apt-get update && apt-get install -y git"},
			Entrypoint:  []string{"codex-acp", "--listen", ":8080"},
		},
	}

	contribs := &plugin.Contributions{
		Runtime: plugin.RuntimeContrib{
			ExtraBuilds: []string{"RUN npm install -g some-tool"},
		},
	}

	output, err := BuildDockerfile(cfg, contribs, ".build/entrypoint.sh", nil)
	require.NoError(t, err)

	assert.Contains(t, output, "FROM node:24-slim")
	assert.Contains(t, output, "RUN apt-get update && apt-get install -y git")
	assert.Contains(t, output, "RUN npm install -g some-tool")
	assert.Contains(t, output, `CMD ["codex-acp","--listen",":8080"]`)
	assert.Contains(t, output, "COPY .build/entrypoint.sh")
	assert.Contains(t, output, "RUN useradd -m -s /bin/bash agent")
}

func TestBuildDockerfile_BuiltinPreset(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Image:      "@builtin/codex",
			Entrypoint: []string{"sleep", "infinity"},
		},
	}

	output, err := BuildDockerfile(cfg, nil, ".build/entrypoint.sh", testPresets)
	require.NoError(t, err)

	assert.Contains(t, output, "FROM node:24-slim")
	assert.Contains(t, output, "npm install -g @openai/codex")
	assert.Contains(t, output, `CMD ["sleep","infinity"]`)
}

func TestBuildDockerfile_CustomImage(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Image:      "python:3.12-slim",
			Entrypoint: []string{"python", "main.py"},
		},
	}

	output, err := BuildDockerfile(cfg, nil, ".build/entrypoint.sh", nil)
	require.NoError(t, err)

	assert.Contains(t, output, "FROM python:3.12-slim")
	assert.Contains(t, output, `CMD ["python","main.py"]`)
	assert.NotContains(t, output, "npm install")
}

func TestBuildDockerfile_PresetDefaultCMD(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Image: "@builtin/pi",
		},
	}

	output, err := BuildDockerfile(cfg, nil, ".build/entrypoint.sh", testPresets)
	require.NoError(t, err)

	assert.Contains(t, output, "FROM node:24-slim")
	assert.Contains(t, output, "pi-coding-agent")
	assert.Contains(t, output, `CMD ["sleep","infinity"]`)
}

func TestEntrypointScript_NoPreEntrypoint(t *testing.T) {
	script := EntrypointScript(nil, "/home/agent/workspace")
	assert.Contains(t, script, `exec gosu "$AGENT_USER" "$@"`)
	assert.Contains(t, script, `${GATEWAY_HOST:=gateway}`)
	assert.Contains(t, script, "/home/agent/workspace")
	assert.NotContains(t, script, "pre-entrypoint")
}

func TestEntrypointScript_WithPreEntrypoint(t *testing.T) {
	cmds := []string{"/usr/sbin/sshd -p 2222", "/usr/bin/other-daemon"}
	script := EntrypointScript(cmds, "/home/agent/workspace")

	assert.Contains(t, script, "/usr/sbin/sshd -p 2222")
	assert.Contains(t, script, "/usr/bin/other-daemon")
	assert.Contains(t, script, "# Plugin pre-entrypoint commands")
	// pre_entrypoint must come before exec
	sshdIdx := indexOf(script, "/usr/sbin/sshd -p 2222")
	execIdx := indexOf(script, `exec gosu "$AGENT_USER" "$@"`)
	assert.Greater(t, execIdx, sshdIdx, "pre_entrypoint commands must come before exec")
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
