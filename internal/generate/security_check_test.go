package generate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateOutput_SkipsNonGateway(t *testing.T) {
	g := &Generator{
		Gateway: false,
	}
	// Should be a no-op — no files needed, no error
	err := g.validateOutput()
	assert.NoError(t, err)
}

func TestValidateOutput_PassesValidGatewaySetup(t *testing.T) {
	outDir := t.TempDir()
	srcDir := t.TempDir()

	// Create minimal gateway source
	gwDir := filepath.Join(srcDir, "gateway")
	require.NoError(t, os.MkdirAll(filepath.Join(gwDir, "cmd", "gateway"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(gwDir, "go.mod"), []byte("module gateway\ngo 1.26\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(gwDir, "cmd", "gateway", "main.go"), []byte("package main\nfunc main() {}\n"), 0644))

	g := &Generator{
		Config: &config.AgentConfig{
			Name:    "coder",
			Runtime: "codex",
			Features: []config.FeatureEntry{
				{Plugin: "telegram", Config: map[string]any{"bot_token": "${TELEGRAM_BOT_TOKEN}"}},
			},
		},
		Runtime: &resolve.RuntimeConfig{
			Name:      "codex",
			BaseImage: "node:22-slim",
			Install:   []string{"npm install -g @openai/codex@latest"},
			Cmd:       []string{"sleep", "infinity"},
			User:      "agent",
		},
		Features: []*resolve.FeatureContributions{
			{MITMDomains: []string{"api.telegram.org"}},
		},
		Gateway: true,
		GatewaySpec: GatewaySpec{
			BuildImage: "golang:1.26.4-alpine",
			BinaryPath: "/gateway",
			ListenPort: 8443,
			DNSPort:    5353,
		},
		Dir:    srcDir,
		OutDir: outDir,
	}

	// Generate first, then validate runs as part of Run()
	err := g.Run()
	require.NoError(t, err)
}

func TestCheckAgentNetworkIsolation(t *testing.T) {
	g := &Generator{
		Config: &config.AgentConfig{Name: "coder"},
	}

	t.Run("passes when agent has internal only", func(t *testing.T) {
		compose := `services:
  coder-gateway:
    networks:
      internal:
      default:
  coder:
    networks:
      internal:
    cap_add:
      - NET_ADMIN
`
		check := g.checkAgentNetworkIsolation(compose)
		assert.True(t, check.Passed)
	})

	t.Run("fails when agent has default network", func(t *testing.T) {
		compose := `services:
  coder-gateway:
    networks:
      internal:
      default:
  coder:
    networks:
      internal:
      default:
    cap_add:
      - NET_ADMIN
`
		check := g.checkAgentNetworkIsolation(compose)
		assert.False(t, check.Passed)
		assert.Contains(t, check.Detail, "internal network only")
	})

	t.Run("fails when agent missing internal network", func(t *testing.T) {
		compose := `services:
  coder:
    networks:
      default:
`
		check := g.checkAgentNetworkIsolation(compose)
		assert.False(t, check.Passed)
	})
}

func TestCheckSecretIsolation(t *testing.T) {
	g := &Generator{
		Config: &config.AgentConfig{Name: "coder"},
	}

	t.Run("passes when agent env has no secrets", func(t *testing.T) {
		compose := `services:
  coder-gateway:
    environment:
      - TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN}
  coder:
    environment:
      - LOG_LEVEL=info
      - GATEWAY_HOST=coder-gateway
      - GH_TOKEN=dummy
`
		check := g.checkSecretIsolation(compose)
		assert.True(t, check.Passed)
		assert.Contains(t, check.Detail, "no credentials")
	})

	t.Run("fails when agent env has ${VAR} pattern", func(t *testing.T) {
		compose := `services:
  coder:
    environment:
      - LOG_LEVEL=info
      - GITHUB_PAT=${GITHUB_PAT}
      - GATEWAY_HOST=coder-gateway
`
		check := g.checkSecretIsolation(compose)
		assert.False(t, check.Passed)
		assert.Contains(t, check.Detail, "${GITHUB_PAT}")
	})

	t.Run("detects multiple leaked secrets", func(t *testing.T) {
		compose := `services:
  coder:
    environment:
      - API_KEY=${API_KEY}
      - SECRET=${SECRET}
`
		check := g.checkSecretIsolation(compose)
		assert.False(t, check.Passed)
		assert.Contains(t, check.Detail, "${API_KEY}")
		assert.Contains(t, check.Detail, "${SECRET}")
	})
}

func TestCheckGatewayCredentials(t *testing.T) {
	t.Run("passes when gateway has credentials", func(t *testing.T) {
		g := &Generator{
			Config: &config.AgentConfig{
				Name: "coder",
				Features: []config.FeatureEntry{
					{Plugin: "telegram", Config: map[string]any{"bot_token": "${TELEGRAM_BOT_TOKEN}"}},
				},
			},
		}
		compose := `services:
  coder-gateway:
    environment:
      - LOG_LEVEL=info
      - TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN}
  coder:
    environment:
      - LOG_LEVEL=info
`
		check := g.checkGatewayCredentials(compose)
		assert.True(t, check.Passed)
		assert.Contains(t, check.Detail, "1 credential(s)")
	})

	t.Run("passes when no credentials configured at all", func(t *testing.T) {
		g := &Generator{
			Config: &config.AgentConfig{Name: "coder"},
		}
		compose := `services:
  coder-gateway:
    environment:
      - LOG_LEVEL=info
  coder:
    environment:
      - LOG_LEVEL=info
`
		check := g.checkGatewayCredentials(compose)
		assert.True(t, check.Passed)
		assert.Contains(t, check.Detail, "none expected")
	})

	t.Run("fails when credentials expected but gateway has none", func(t *testing.T) {
		g := &Generator{
			Config: &config.AgentConfig{
				Name: "coder",
				Features: []config.FeatureEntry{
					{Plugin: "github", Config: map[string]any{"token": "${GITHUB_PAT}"}},
				},
			},
		}
		compose := `services:
  coder-gateway:
    environment:
      - LOG_LEVEL=info
  coder:
    environment:
      - LOG_LEVEL=info
`
		check := g.checkGatewayCredentials(compose)
		assert.False(t, check.Passed)
		assert.Contains(t, check.Detail, "should hold credentials")
	})
}

func TestCheckRouteEnforcement(t *testing.T) {
	g := &Generator{}

	t.Run("passes when route replacement present", func(t *testing.T) {
		entrypoint := `#!/bin/bash
GATEWAY_IP=$(getent hosts $GATEWAY_HOST | awk '{print $1}')
ip route replace default via $GATEWAY_IP
exec su -c 'sleep infinity' agent
`
		check := g.checkRouteEnforcement(entrypoint)
		assert.True(t, check.Passed)
	})

	t.Run("fails when route replacement missing", func(t *testing.T) {
		entrypoint := `#!/bin/bash
exec su -c 'sleep infinity' agent
`
		check := g.checkRouteEnforcement(entrypoint)
		assert.False(t, check.Passed)
		assert.Contains(t, check.Detail, "ip route replace default via")
	})
}

func TestCheckCACertReadOnly(t *testing.T) {
	g := &Generator{
		Config: &config.AgentConfig{Name: "coder"},
	}

	t.Run("passes when shared-certs mounted read-only", func(t *testing.T) {
		compose := `services:
  coder:
    volumes:
      - shared-certs:/usr/local/share/ca-certificates:ro
      - agent-home:/home/agent
`
		check := g.checkCACertReadOnly(compose)
		assert.True(t, check.Passed)
	})

	t.Run("fails when shared-certs mounted read-write", func(t *testing.T) {
		compose := `services:
  coder:
    volumes:
      - shared-certs:/usr/local/share/ca-certificates
      - agent-home:/home/agent
`
		check := g.checkCACertReadOnly(compose)
		assert.False(t, check.Passed)
		assert.Contains(t, check.Detail, "read-only")
	})
}

func TestCheckNoPrivileged(t *testing.T) {
	g := &Generator{}

	t.Run("passes when no privileged mode", func(t *testing.T) {
		compose := `services:
  coder:
    cap_add:
      - NET_ADMIN
`
		check := g.checkNoPrivileged(compose)
		assert.True(t, check.Passed)
	})

	t.Run("fails when privileged mode present", func(t *testing.T) {
		compose := `services:
  coder:
    privileged: true
`
		check := g.checkNoPrivileged(compose)
		assert.False(t, check.Passed)
		assert.Contains(t, check.Detail, "privileged: true")
	})
}

func TestExtractServiceSection(t *testing.T) {
	compose := `services:
  coder-gateway:
    build:
      context: .
    networks:
      internal:
      default:
    environment:
      - TOKEN=${TOKEN}
  coder:
    build:
      context: .
    networks:
      internal:
    environment:
      - LOG_LEVEL=info

networks:
  internal:
    internal: true
`

	t.Run("extracts gateway section", func(t *testing.T) {
		section := extractServiceSection(compose, "coder-gateway")
		assert.Contains(t, section, "default:")
		assert.Contains(t, section, "TOKEN=${TOKEN}")
		assert.NotContains(t, section, "LOG_LEVEL=info")
	})

	t.Run("extracts agent section", func(t *testing.T) {
		section := extractServiceSection(compose, "coder")
		assert.Contains(t, section, "LOG_LEVEL=info")
		assert.NotContains(t, section, "TOKEN=${TOKEN}")
		assert.NotContains(t, section, "internal: true") // from networks: block
	})

	t.Run("returns empty for unknown service", func(t *testing.T) {
		section := extractServiceSection(compose, "nonexistent")
		assert.Empty(t, section)
	})
}

func TestExtractEnvironmentSection(t *testing.T) {
	serviceSection := `    build:
      context: .
    environment:
      - LOG_LEVEL=info
      - GATEWAY_HOST=coder-gateway
      - SECRET=${SECRET}
    networks:
      internal:
`

	env := extractEnvironmentSection(serviceSection)
	assert.Contains(t, env, "LOG_LEVEL=info")
	assert.Contains(t, env, "SECRET=${SECRET}")
	assert.NotContains(t, env, "internal:")
	assert.NotContains(t, env, "build:")
}

func TestValidateOutput_FailsOnViolation(t *testing.T) {
	outDir := t.TempDir()

	// Write a compose file with a security violation: agent has ${SECRET}
	composeContent := `services:
  coder-gateway:
    build:
      context: .
      dockerfile: Dockerfile.gateway
    networks:
      internal:
      default:
    environment:
      - LOG_LEVEL=info
      - SECRET=${SECRET}
  coder:
    build:
      context: .
      dockerfile: Dockerfile.agent
    networks:
      internal:
    environment:
      - LOG_LEVEL=info
      - LEAKED=${LEAKED}
    cap_add:
      - NET_ADMIN

networks:
  internal:
    internal: true
`
	entrypointContent := `#!/bin/bash
GATEWAY_IP=$(getent hosts $GATEWAY_HOST | awk '{print $1}')
ip route replace default via $GATEWAY_IP
exec su -c 'sleep infinity' agent
`
	require.NoError(t, os.WriteFile(filepath.Join(outDir, "docker-compose.yml"), []byte(composeContent), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(outDir, "entrypoint.sh"), []byte(entrypointContent), 0755))

	g := &Generator{
		Config:  &config.AgentConfig{Name: "coder"},
		Gateway: true,
		OutDir:  outDir,
	}

	err := g.validateOutput()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Secret isolation")
	assert.Contains(t, err.Error(), "${LEAKED}")
}
