package generate

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerfileBuilder_Filename(t *testing.T) {
	tests := []struct {
		variant  string
		expected string
	}{
		{"single", "Dockerfile"},
		{"gateway", "Dockerfile.gateway"},
		{"agent", "Dockerfile.agent"},
	}
	for _, tt := range tests {
		t.Run(tt.variant, func(t *testing.T) {
			b := &DockerfileBuilder{Variant: tt.variant}
			assert.Equal(t, tt.expected, b.Filename())
		})
	}
}

func TestDockerfileBuilder_Render_Single(t *testing.T) {
	t.Run("basic single stage", func(t *testing.T) {
		g := &Generator{
			Config:  &config.AgentConfig{Name: "test"},
			Runtime: &resolve.RuntimeConfig{
				BaseImage: "node:22-slim",
				User:      "agent",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
			},
		}
		b := NewDockerfileBuilder(g, "single")
		content, err := b.Render()
		require.NoError(t, err)

		assert.Contains(t, content, "FROM node:22-slim")
		assert.Contains(t, content, "RUN useradd -m -s /bin/bash agent")
		assert.Contains(t, content, "RUN npm install -g @openai/codex@latest")
		assert.Contains(t, content, "USER agent")
		assert.Contains(t, content, `CMD ["sleep", "infinity"]`)
		assert.NotContains(t, content, "ENTRYPOINT")
	})

	t.Run("with hooks triggers entrypoint", func(t *testing.T) {
		g := &Generator{
			Config:  &config.AgentConfig{Name: "test"},
			Runtime: &resolve.RuntimeConfig{
				BaseImage: "node:22-slim",
				User:      "agent",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
			},
			Features: []*resolve.FeatureContributions{
				{EntrypointHooks: []string{"scripts/setup.sh"}},
			},
		}
		b := NewDockerfileBuilder(g, "single")
		content, err := b.Render()
		require.NoError(t, err)

		assert.Contains(t, content, `ENTRYPOINT ["/opt/entrypoint.sh"]`)
		assert.NotContains(t, content, "CMD")
		assert.Contains(t, content, "COPY hooks/ /opt/hooks/")
		assert.Contains(t, content, "COPY entrypoint.sh /opt/entrypoint.sh")
	})

	t.Run("with home override", func(t *testing.T) {
		g := &Generator{
			Config:  &config.AgentConfig{Name: "test"},
			Runtime: &resolve.RuntimeConfig{
				BaseImage: "node:22-slim",
				User:      "agent",
				Cmd:       []string{"sleep", "infinity"},
			},
			Features: []*resolve.FeatureContributions{
				{HomeOverride: "home"},
			},
		}
		b := NewDockerfileBuilder(g, "single")
		content, err := b.Render()
		require.NoError(t, err)

		assert.Contains(t, content, "COPY home-override/ /opt/home-override/")
		assert.Contains(t, content, `ENTRYPOINT ["/opt/entrypoint.sh"]`)
	})

	t.Run("with feature commands", func(t *testing.T) {
		g := &Generator{
			Config:  &config.AgentConfig{Name: "test"},
			Runtime: &resolve.RuntimeConfig{
				BaseImage: "node:22-slim",
				User:      "agent",
				Cmd:       []string{"sleep", "infinity"},
			},
			Features: []*resolve.FeatureContributions{
				{Commands: []string{"apt-get update && apt-get install -y ripgrep"}},
			},
		}
		b := NewDockerfileBuilder(g, "single")
		content, err := b.Render()
		require.NoError(t, err)

		assert.Contains(t, content, "RUN apt-get update && apt-get install -y ripgrep")
	})
}

func TestDockerfileBuilder_Render_Gateway(t *testing.T) {
	t.Run("basic gateway", func(t *testing.T) {
		g := &Generator{
			Config:  &config.AgentConfig{Name: "test"},
			Runtime: &resolve.RuntimeConfig{BaseImage: "node:22-slim", User: "agent"},
			Gateway: true,
			GatewaySpec: GatewaySpec{
				BuildImage: "golang:1.26.4-alpine",
				BinaryPath: "/gateway",
				ListenPort: 8443,
				DNSPort:    5353,
			},
		}
		b := NewDockerfileBuilder(g, "gateway")
		content, err := b.Render()
		require.NoError(t, err)

		assert.Contains(t, content, "FROM golang:1.26.4-alpine AS builder")
		assert.Contains(t, content, "COPY gateway-src/ .")
		assert.Contains(t, content, "RUN go mod tidy && go build -o /gateway ./cmd/gateway/")
		assert.Contains(t, content, "FROM alpine:3.20")
		assert.Contains(t, content, "COPY --from=builder /gateway /usr/local/bin/gateway")
		assert.Contains(t, content, `ENTRYPOINT ["/opt/entrypoint.sh"]`)
		assert.NotContains(t, content, "mkdir -p /shared/certs")
	})

	t.Run("with MITM domains", func(t *testing.T) {
		g := &Generator{
			Config:  &config.AgentConfig{Name: "test"},
			Runtime: &resolve.RuntimeConfig{BaseImage: "node:22-slim", User: "agent"},
			Gateway: true,
			GatewaySpec: GatewaySpec{
				BuildImage: "golang:1.26.4-alpine",
				BinaryPath: "/gateway",
				ListenPort: 8443,
				DNSPort:    5353,
			},
			Features: []*resolve.FeatureContributions{
				{MITMDomains: []string{"api.telegram.org"}},
			},
		}
		b := NewDockerfileBuilder(g, "gateway")
		content, err := b.Render()
		require.NoError(t, err)

		assert.Contains(t, content, "mkdir -p /shared/certs /etc/gateway/private")
	})
}

func TestDockerfileBuilder_Render_Agent(t *testing.T) {
	t.Run("basic agent without channel manager", func(t *testing.T) {
		g := &Generator{
			Config:  &config.AgentConfig{Name: "test"},
			Runtime: &resolve.RuntimeConfig{
				BaseImage: "node:22-slim",
				User:      "agent",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
			},
			Gateway: true,
			GatewaySpec: GatewaySpec{
				BuildImage: "golang:1.26.4-alpine",
				BinaryPath: "/gateway",
				ListenPort: 8443,
				DNSPort:    5353,
			},
		}
		b := NewDockerfileBuilder(g, "agent")
		content, err := b.Render()
		require.NoError(t, err)

		assert.Contains(t, content, "FROM node:22-slim")
		assert.Contains(t, content, "iproute2")
		assert.Contains(t, content, "iptables")
		assert.Contains(t, content, "useradd -m -s /bin/bash agent")
		assert.Contains(t, content, "RUN npm install -g @openai/codex@latest")
		assert.Contains(t, content, `ENTRYPOINT ["/opt/entrypoint.sh"]`)
		assert.NotContains(t, content, "channel-manager-build")
	})

	t.Run("with channel manager", func(t *testing.T) {
		g := &Generator{
			Config:  &config.AgentConfig{Name: "test"},
			Runtime: &resolve.RuntimeConfig{
				BaseImage: "node:22-slim",
				User:      "agent",
				Install:   []string{"npm install -g @openai/codex@latest"},
				Cmd:       []string{"sleep", "infinity"},
			},
			Gateway:        true,
			ChannelManager: true,
			GatewaySpec: GatewaySpec{
				BuildImage: "golang:1.26.4-alpine",
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
			Features: []*resolve.FeatureContributions{
				{ChannelName: "telegram", MITMDomains: []string{"api.telegram.org"}},
			},
		}
		b := NewDockerfileBuilder(g, "agent")
		content, err := b.Render()
		require.NoError(t, err)

		assert.Contains(t, content, "FROM node:22-slim AS channel-manager-build")
		assert.Contains(t, content, "RUN npm install")
		assert.Contains(t, content, "RUN npm run build")
		assert.Contains(t, content, "COPY --from=channel-manager-build /src/dist/ /opt/channel-manager/dist/")
		assert.Contains(t, content, "COPY channel-manager-config.json /opt/channel-manager/config.json")

		// Layer order: runtime install before channel-manager COPY
		installIdx := indexOf(content, "npm install -g @openai/codex")
		cmCopyIdx := indexOf(content, "COPY --from=channel-manager-build /src/dist/")
		require.Greater(t, installIdx, -1)
		require.Greater(t, cmCopyIdx, -1)
		assert.Less(t, installIdx, cmCopyIdx)
	})

	t.Run("with volume paths", func(t *testing.T) {
		g := &Generator{
			Config:  &config.AgentConfig{Name: "test"},
			Runtime: &resolve.RuntimeConfig{
				BaseImage: "node:22-slim",
				User:      "agent",
				Cmd:       []string{"sleep", "infinity"},
			},
			Gateway: true,
			GatewaySpec: GatewaySpec{
				BuildImage: "golang:1.26.4-alpine",
				BinaryPath: "/gateway",
				ListenPort: 8443,
				DNSPort:    5353,
			},
			Features: []*resolve.FeatureContributions{
				{Volumes: []string{"agent-home:/home/agent"}},
			},
		}
		b := NewDockerfileBuilder(g, "agent")
		content, err := b.Render()
		require.NoError(t, err)

		assert.Contains(t, content, "mkdir -p /home/agent")
		assert.Contains(t, content, "chown -R agent:agent /home/agent")
	})
}

func TestNewDockerfileBuilder_PopulatesFields(t *testing.T) {
	g := &Generator{
		Config: &config.AgentConfig{Name: "test"},
		Runtime: &resolve.RuntimeConfig{
			BaseImage: "node:22-slim",
			User:      "agent",
			Install:   []string{"npm install -g foo"},
			Cmd:       []string{"sleep", "infinity"},
		},
		Gateway:        true,
		ChannelManager: true,
		GatewaySpec: GatewaySpec{
			BuildImage: "golang:1.26.4-alpine",
			BinaryPath: "/gateway",
			ListenPort: 8443,
			DNSPort:    5353,
		},
		ChannelManagerSpec: ChannelManagerSpec{
			BuildImage: "node:22-slim",
			InstallCmd: "npm ci",
			BuildCmd:   "npm run build",
			DistDir:    "/src/dist",
			EntryPoint: "node index.js",
		},
		Features: []*resolve.FeatureContributions{
			{
				Commands:    []string{"apt-get install -y git"},
				MITMDomains: []string{"example.com"},
				ChannelName: "test-channel",
			},
		},
	}

	t.Run("gateway variant", func(t *testing.T) {
		b := NewDockerfileBuilder(g, "gateway")
		assert.Equal(t, "golang:1.26.4-alpine", b.GatewayBuildImage)
		assert.Equal(t, "/gateway", b.GatewayBinaryPath)
		assert.True(t, b.HasMITM)
		assert.False(t, b.ChannelManager)
	})

	t.Run("agent variant", func(t *testing.T) {
		b := NewDockerfileBuilder(g, "agent")
		assert.True(t, b.ChannelManager)
		assert.Equal(t, "node:22-slim", b.CMBuildImage)
		assert.Equal(t, "npm ci", b.CMInstallCmd)
		assert.Equal(t, "npm run build", b.CMBuildCmd)
		assert.Equal(t, "/src/dist", b.CMDistDir)
		assert.Equal(t, []string{"apt-get install -y git"}, b.FeatureCmds)
	})

	t.Run("single variant", func(t *testing.T) {
		b := NewDockerfileBuilder(g, "single")
		assert.Equal(t, "node:22-slim", b.BaseImage)
		assert.Equal(t, "agent", b.User)
		assert.Equal(t, []string{"npm install -g foo"}, b.Install)
		assert.Equal(t, []string{"sleep", "infinity"}, b.Cmd)
		assert.True(t, b.HasEntrypoint)
	})
}
