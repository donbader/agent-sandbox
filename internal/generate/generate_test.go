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

func TestGenerator_Run(t *testing.T) {
	t.Run("basic codex agent", func(t *testing.T) {
		outDir := t.TempDir()
		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
			},
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
			},
			Dir:    t.TempDir(),
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		// Check Dockerfile
		df, err := os.ReadFile(filepath.Join(outDir, "Dockerfile"))
		require.NoError(t, err)
		assert.Contains(t, string(df), "FROM node:22-slim")
		assert.Contains(t, string(df), "npm install -g @openai/codex")
		assert.Contains(t, string(df), "USER agent")
		assert.Contains(t, string(df), `CMD ["sleep", "infinity"]`)
		assert.NotContains(t, string(df), "ENTRYPOINT")

		// Check docker-compose.yml
		dc, err := os.ReadFile(filepath.Join(outDir, "docker-compose.yml"))
		require.NoError(t, err)
		assert.Contains(t, string(dc), "coder:")
		assert.Contains(t, string(dc), "build:")
		assert.Contains(t, string(dc), "coder:")
	})

	t.Run("with feature commands", func(t *testing.T) {
		outDir := t.TempDir()
		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
			},
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
			},
			Features: []*resolve.FeatureContributions{
				{Commands: []string{"apt-get install -y ripgrep fd-find"}},
			},
			Dir:    t.TempDir(),
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		df, err := os.ReadFile(filepath.Join(outDir, "Dockerfile"))
		require.NoError(t, err)
		assert.Contains(t, string(df), "RUN apt-get install -y ripgrep fd-find")
	})

	t.Run("with entrypoint hooks", func(t *testing.T) {
		srcDir := t.TempDir()
		outDir := t.TempDir()

		// Create a hook script in the source dir
		scriptsDir := filepath.Join(srcDir, "scripts")
		require.NoError(t, os.MkdirAll(scriptsDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(scriptsDir, "setup.sh"), []byte("#!/bin/bash\necho setup"), 0755))

		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
			},
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
			},
			Features: []*resolve.FeatureContributions{
				{EntrypointHooks: []string{"scripts/setup.sh"}},
			},
			Dir:    srcDir,
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		// Dockerfile should have ENTRYPOINT instead of CMD
		df, err := os.ReadFile(filepath.Join(outDir, "Dockerfile"))
		require.NoError(t, err)
		assert.Contains(t, string(df), `ENTRYPOINT ["/opt/entrypoint.sh"]`)
		assert.NotContains(t, string(df), "CMD")
		assert.Contains(t, string(df), "COPY hooks/ /opt/hooks/")

		// Entrypoint script should exist
		ep, err := os.ReadFile(filepath.Join(outDir, "entrypoint.sh"))
		require.NoError(t, err)
		assert.Contains(t, string(ep), "/opt/hooks/setup.sh")
		assert.Contains(t, string(ep), "exec su -c 'sleep infinity' agent")

		// Hook should be copied
		hook, err := os.ReadFile(filepath.Join(outDir, "hooks", "setup.sh"))
		require.NoError(t, err)
		assert.Contains(t, string(hook), "echo setup")
	})

	t.Run("with home override", func(t *testing.T) {
		srcDir := t.TempDir()
		outDir := t.TempDir()

		// Create home override directory
		homeDir := filepath.Join(srcDir, "home")
		require.NoError(t, os.MkdirAll(homeDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(homeDir, ".gitconfig"), []byte("[user]\n  name = Agent"), 0644))

		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
			},
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
			},
			Features: []*resolve.FeatureContributions{
				{HomeOverride: "home"},
			},
			Dir:    srcDir,
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		// Dockerfile should COPY home-override
		df, err := os.ReadFile(filepath.Join(outDir, "Dockerfile"))
		require.NoError(t, err)
		assert.Contains(t, string(df), "COPY home-override/ /opt/home-override/")

		// Entrypoint should copy home override
		ep, err := os.ReadFile(filepath.Join(outDir, "entrypoint.sh"))
		require.NoError(t, err)
		assert.Contains(t, string(ep), "cp -rT /opt/home-override /home/agent")

		// Home override files should be copied
		gc, err := os.ReadFile(filepath.Join(outDir, "home-override", ".gitconfig"))
		require.NoError(t, err)
		assert.Contains(t, string(gc), "name = Agent")
	})

	t.Run("with volumes", func(t *testing.T) {
		outDir := t.TempDir()
		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
			},
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
			},
			Features: []*resolve.FeatureContributions{
				{Volumes: []string{"agent-home:/home/agent"}},
			},
			Dir:    t.TempDir(),
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		dc, err := os.ReadFile(filepath.Join(outDir, "docker-compose.yml"))
		require.NoError(t, err)
		assert.Contains(t, string(dc), "agent-home:/home/agent")
		assert.Contains(t, string(dc), "volumes:")
		assert.Contains(t, string(dc), "  agent-home:")
	})

	t.Run("with env vars", func(t *testing.T) {
		outDir := t.TempDir()
		srcDir := t.TempDir()
		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
				Features: []config.FeatureEntry{
					{Plugin: "github", Config: map[string]any{"token": "${GITHUB_PAT}"}},
				},
			},
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
			},
			Dir:    srcDir,
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		env, err := os.ReadFile(filepath.Join(srcDir, ".env.example"))
		require.NoError(t, err)
		assert.Contains(t, string(env), "GITHUB_PAT=")
	})

	t.Run("no features no env", func(t *testing.T) {
		outDir := t.TempDir()
		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
			},
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
			},
			Dir:    t.TempDir(),
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		_, err = os.Stat(filepath.Join(outDir, ".env.example"))
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("with runtime ports", func(t *testing.T) {
		outDir := t.TempDir()
		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
			},
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
				Ports:     []string{"1455:1455"},
			},
			Dir:    t.TempDir(),
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		dc, err := os.ReadFile(filepath.Join(outDir, "docker-compose.yml"))
		require.NoError(t, err)
		assert.Contains(t, string(dc), "ports:")
		assert.Contains(t, string(dc), "1455:1455")
	})

	t.Run("with gateway", func(t *testing.T) {
		srcDir := t.TempDir()
		outDir := t.TempDir()

		// Create minimal gateway source in the project dir
		gwDir := filepath.Join(srcDir, "gateway")
		require.NoError(t, os.MkdirAll(filepath.Join(gwDir, "cmd", "gateway"), 0755))
		require.NoError(t, os.MkdirAll(filepath.Join(gwDir, "internal", "proxy"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(gwDir, "go.mod"), []byte("module gateway\ngo 1.24\n"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(gwDir, "cmd", "gateway", "main.go"), []byte("package main\nfunc main() {}\n"), 0644))

		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
			},
			Runtime: &resolve.RuntimeConfig{
				Name:      "codex",
				BaseImage: "node:22-slim",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
				User:      "agent",
			},
			Gateway: true,
			GatewaySpec: GatewaySpec{
				BuildImage: "golang:1.24-alpine",
				BinaryPath: "/gateway",
				ListenPort: 8443,
				DNSPort:    5353,
			},
			Dir:    srcDir,
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		// Dockerfile.gateway should build the gateway binary
		dfGw, err := os.ReadFile(filepath.Join(outDir, "Dockerfile.gateway"))
		require.NoError(t, err)
		dfGwStr := string(dfGw)
		assert.Contains(t, dfGwStr, "FROM golang:1.24-alpine AS builder")
		assert.Contains(t, dfGwStr, "COPY gateway-src/ .")
		assert.Contains(t, dfGwStr, "RUN go mod tidy && go build -o /gateway ./cmd/gateway/")
		assert.Contains(t, dfGwStr, "COPY --from=builder /gateway /usr/local/bin/gateway")
		assert.Contains(t, dfGwStr, `ENTRYPOINT ["/opt/entrypoint.sh"]`)

		// Dockerfile.agent should have runtime with iptables, no gateway binary
		dfAgent, err := os.ReadFile(filepath.Join(outDir, "Dockerfile.agent"))
		require.NoError(t, err)
		dfAgentStr := string(dfAgent)
		assert.Contains(t, dfAgentStr, "FROM node:22-slim")
		assert.Contains(t, dfAgentStr, "iproute2")
		assert.Contains(t, dfAgentStr, "iptables")
		assert.Contains(t, dfAgentStr, "useradd -m -s /bin/bash agent")
		assert.Contains(t, dfAgentStr, `ENTRYPOINT ["/opt/entrypoint.sh"]`)
		assert.NotContains(t, dfAgentStr, "useradd -r -s /bin/false gateway")
		assert.NotContains(t, dfAgentStr, "gateway-build")

		// gateway-entrypoint.sh should just exec the gateway binary
		gwEp, err := os.ReadFile(filepath.Join(outDir, "gateway-entrypoint.sh"))
		require.NoError(t, err)
		assert.Contains(t, string(gwEp), "exec /usr/local/bin/gateway")

		// Agent entrypoint should use DNAT to redirect to gateway container, not start gateway
		ep, err := os.ReadFile(filepath.Join(outDir, "entrypoint.sh"))
		require.NoError(t, err)
		epStr := string(ep)
		assert.Contains(t, epStr, "ip route replace default")
		assert.Contains(t, epStr, "getent hosts $GATEWAY_HOST")
		assert.Contains(t, epStr, "nameserver $GATEWAY_IP")
		assert.Contains(t, epStr, "/etc/resolv.conf")
		assert.NotContains(t, epStr, "--to-port 8443")
		assert.NotContains(t, epStr, "/usr/local/bin/gateway")
		assert.Contains(t, epStr, "exec su -c 'sleep infinity' agent")

		// docker-compose.yml should have two services with internal network
		dc, err := os.ReadFile(filepath.Join(outDir, "docker-compose.yml"))
		require.NoError(t, err)
		dcStr := string(dc)
		assert.Contains(t, dcStr, "coder-gateway:")
		assert.Contains(t, dcStr, "Dockerfile.gateway")
		assert.Contains(t, dcStr, "coder:")
		assert.Contains(t, dcStr, "Dockerfile.agent")
		assert.Contains(t, dcStr, "NET_ADMIN")
		assert.Contains(t, dcStr, "internal:")
		assert.Contains(t, dcStr, "GATEWAY_HOST=coder-gateway")
		assert.Contains(t, dcStr, "depends_on:")

		// Gateway source should be copied
		_, err = os.Stat(filepath.Join(outDir, "gateway-src", "go.mod"))
		assert.NoError(t, err)

		// Gateway config should exist
		gwCfg, err := os.ReadFile(filepath.Join(outDir, "gateway-config.yaml"))
		require.NoError(t, err)
		assert.Contains(t, string(gwCfg), "listen:")
	})

	t.Run("with gateway and channel-manager (telegram)", func(t *testing.T) {
		srcDir := t.TempDir()
		outDir := t.TempDir()

		// Create minimal gateway source in the project dir
		gwDir := filepath.Join(srcDir, "gateway")
		require.NoError(t, os.MkdirAll(filepath.Join(gwDir, "cmd", "gateway"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(gwDir, "go.mod"), []byte("module gateway\ngo 1.24\n"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(gwDir, "cmd", "gateway", "main.go"), []byte("package main\nfunc main() {}\n"), 0644))

		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "coder",
				Runtime: "codex",
				Features: []config.FeatureEntry{
					{Plugin: "telegram", Config: map[string]any{"access_control": map[string]any{"allowed_users": []any{"@testuser"}, "require_mention": false}}},
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
				{
					MITMDomains:   []string{"api.telegram.org"},
					ChannelName: "telegram",
					EnvVars:       []string{"TELEGRAM_BOT_TOKEN"},
					ChannelConfig:  map[string]any{"access_control": map[string]any{"allowed_users": []any{"@testuser"}}},
				},
			},
			Gateway: true,
			Bridge:  true,
			GatewaySpec: GatewaySpec{
				BuildImage: "golang:1.24-alpine",
				BinaryPath: "/gateway",
				ListenPort: 8443,
				DNSPort:    5353,
			},
			ChannelManagerSpec: ChannelManagerSpec{
				BuildImage: "node:22-slim",
				InstallCmd: "npm install",
				BuildCmd:   "npm run build",
				DistDir:    "/src/dist",
				EntryPoint: "node /opt/channel-manager/dist/index.js",
			},
			Dir:    srcDir,
			OutDir: outDir,
		}

		err := g.Run()
		require.NoError(t, err)

		// Dockerfile.agent should have channel-manager build stage and CA cert
		dfAgent, err := os.ReadFile(filepath.Join(outDir, "Dockerfile.agent"))
		require.NoError(t, err)
		dfAgentStr := string(dfAgent)
		assert.Contains(t, dfAgentStr, "FROM node:22-slim AS channel-manager-build")
		assert.Contains(t, dfAgentStr, "RUN npm install")
		assert.Contains(t, dfAgentStr, "RUN npm run build")
		assert.Contains(t, dfAgentStr, "COPY --from=channel-manager-build /src/dist/ /opt/channel-manager/dist/")
		assert.Contains(t, dfAgentStr, "COPY channel-manager-config.json /opt/channel-manager/config.json")
		assert.Contains(t, dfAgentStr, "COPY certs/ca.crt /usr/local/share/ca-certificates/sandbox-ca.crt")
		assert.Contains(t, dfAgentStr, "RUN update-ca-certificates")

		// Dockerfile.gateway should exist
		_, err = os.Stat(filepath.Join(outDir, "Dockerfile.gateway"))
		assert.NoError(t, err)

		// Agent entrypoint should use DNAT and start channel-manager
		ep, err := os.ReadFile(filepath.Join(outDir, "entrypoint.sh"))
		require.NoError(t, err)
		epStr := string(ep)
		assert.Contains(t, epStr, "nameserver $GATEWAY_IP")
		assert.Contains(t, epStr, "exec node /opt/channel-manager/dist/index.js")
		assert.NotContains(t, epStr, "exec su -c")

		// Gateway config should have MITM domains
		gwCfg, err := os.ReadFile(filepath.Join(outDir, "gateway-config.yaml"))
		require.NoError(t, err)
		gwCfgStr := string(gwCfg)
		assert.Contains(t, gwCfgStr, "mitm_domains:")
		assert.Contains(t, gwCfgStr, "api.telegram.org")
		assert.Contains(t, gwCfgStr, "ca_cert: /etc/gateway/ca.crt")
		assert.Contains(t, gwCfgStr, "ca_key: /etc/gateway/ca.key")

		// CA cert should be generated
		_, err = os.Stat(filepath.Join(outDir, "certs", "ca.crt"))
		assert.NoError(t, err)
		_, err = os.Stat(filepath.Join(outDir, "certs", "ca.key"))
		assert.NoError(t, err)

		// Bridge config should exist with correct content
		channelCfg, err := os.ReadFile(filepath.Join(outDir, "channel-manager-config.json"))
		require.NoError(t, err)
		channelCfgStr := string(channelCfg)
		assert.Contains(t, channelCfgStr, `"channel": "telegram"`)
		assert.Contains(t, channelCfgStr, `"acp_command"`)
		assert.Contains(t, channelCfgStr, `"access_control"`)
		assert.Contains(t, channelCfgStr, `"allowed_users"`)

		// Bridge source should be copied
		_, err = os.Stat(filepath.Join(outDir, "channel-manager-src", "package.json"))
		assert.NoError(t, err)
		_, err = os.Stat(filepath.Join(outDir, "channel-manager-src", "tsconfig.json"))
		assert.NoError(t, err)

		// docker-compose.yml should have TELEGRAM_BOT_TOKEN
		dc, err := os.ReadFile(filepath.Join(outDir, "docker-compose.yml"))
		require.NoError(t, err)
		assert.Contains(t, string(dc), "TELEGRAM_BOT_TOKEN")

		// .env.example should have TELEGRAM_BOT_TOKEN
		env, err := os.ReadFile(filepath.Join(srcDir, ".env.example"))
		require.NoError(t, err)
		assert.Contains(t, string(env), "TELEGRAM_BOT_TOKEN=")
	})
}
