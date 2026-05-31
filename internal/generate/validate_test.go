package generate

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
)

func TestValidate(t *testing.T) {
	validGenerator := func() *Generator {
		return &Generator{
			Config:  &config.AgentConfig{Name: "test", Runtime: "codex"},
			Runtime: &resolve.RuntimeConfig{BaseImage: "node:22-slim", User: "agent"},
			Dir:     "/tmp/src",
			OutDir:  "/tmp/out",
		}
	}

	t.Run("valid config passes", func(t *testing.T) {
		g := validGenerator()
		assert.NoError(t, g.validate())
	})

	t.Run("nil Config", func(t *testing.T) {
		g := validGenerator()
		g.Config = nil
		assert.ErrorContains(t, g.validate(), "Config is nil")
	})

	t.Run("nil Runtime", func(t *testing.T) {
		g := validGenerator()
		g.Runtime = nil
		assert.ErrorContains(t, g.validate(), "Runtime is nil")
	})

	t.Run("empty base_image", func(t *testing.T) {
		g := validGenerator()
		g.Runtime.BaseImage = ""
		assert.ErrorContains(t, g.validate(), "no base_image")
	})

	t.Run("empty Dir", func(t *testing.T) {
		g := validGenerator()
		g.Dir = ""
		assert.ErrorContains(t, g.validate(), "Dir")
	})

	t.Run("empty OutDir", func(t *testing.T) {
		g := validGenerator()
		g.OutDir = ""
		assert.ErrorContains(t, g.validate(), "OutDir")
	})

	t.Run("MITM domains without gateway", func(t *testing.T) {
		g := validGenerator()
		g.Gateway = false
		g.Features = []*resolve.FeatureContributions{
			{Name: "telegram", MITMDomains: []string{"api.telegram.org"}},
		}
		assert.ErrorContains(t, g.validate(), "gateway is disabled")
	})

	t.Run("BridgeChannel without bridge", func(t *testing.T) {
		g := validGenerator()
		g.Bridge = false
		g.Features = []*resolve.FeatureContributions{
			{Name: "telegram", BridgeChannel: "telegram"},
		}
		assert.ErrorContains(t, g.validate(), "bridge is disabled")
	})

	t.Run("bridge enabled but no channel", func(t *testing.T) {
		g := validGenerator()
		g.Bridge = true
		g.BridgeSpec = BridgeSpec{
			BuildImage: "node:22-slim",
			EntryPoint: "node /opt/bridge/dist/index.js",
		}
		g.Features = []*resolve.FeatureContributions{
			{Name: "custom-runtime"},
		}
		assert.ErrorContains(t, g.validate(), "no feature declares a BridgeChannel")
	})

	t.Run("valid with gateway and bridge", func(t *testing.T) {
		g := validGenerator()
		g.Gateway = true
		g.Bridge = true
		g.GatewaySpec = GatewaySpec{
			BuildImage: "golang:1.24-alpine",
			BinaryPath: "/gateway",
			ListenPort: 8443,
			DNSPort:    5353,
		}
		g.BridgeSpec = BridgeSpec{
			BuildImage: "node:22-slim",
			EntryPoint: "node /opt/bridge/dist/index.js",
		}
		g.Features = []*resolve.FeatureContributions{
			{Name: "telegram", MITMDomains: []string{"api.telegram.org"}, BridgeChannel: "telegram"},
		}
		assert.NoError(t, g.validate())
	})

	t.Run("gateway enabled but GatewaySpec.BuildImage empty", func(t *testing.T) {
		g := validGenerator()
		g.Gateway = true
		g.GatewaySpec = GatewaySpec{BinaryPath: "/gateway", ListenPort: 8443, DNSPort: 5353}
		assert.ErrorContains(t, g.validate(), "GatewaySpec.BuildImage")
	})

	t.Run("gateway enabled but GatewaySpec.ListenPort is 0", func(t *testing.T) {
		g := validGenerator()
		g.Gateway = true
		g.GatewaySpec = GatewaySpec{BuildImage: "golang:1.24-alpine", BinaryPath: "/gateway", DNSPort: 5353}
		assert.ErrorContains(t, g.validate(), "GatewaySpec.ListenPort")
	})

	t.Run("bridge enabled but BridgeSpec.BuildImage empty", func(t *testing.T) {
		g := validGenerator()
		g.Bridge = true
		g.BridgeSpec = BridgeSpec{EntryPoint: "node /opt/bridge/dist/index.js"}
		g.Features = []*resolve.FeatureContributions{
			{Name: "telegram", BridgeChannel: "telegram"},
		}
		assert.ErrorContains(t, g.validate(), "BridgeSpec.BuildImage")
	})

	t.Run("bridge enabled but BridgeSpec.EntryPoint empty", func(t *testing.T) {
		g := validGenerator()
		g.Bridge = true
		g.BridgeSpec = BridgeSpec{BuildImage: "node:22-slim"}
		g.Features = []*resolve.FeatureContributions{
			{Name: "telegram", BridgeChannel: "telegram"},
		}
		assert.ErrorContains(t, g.validate(), "BridgeSpec.EntryPoint")
	})
}
