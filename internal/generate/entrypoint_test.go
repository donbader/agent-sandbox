package generate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEntrypointBuilder_Gateway(t *testing.T) {
	b := &EntrypointBuilder{
		Variant:           "gateway",
		GatewayListenPort: 8443,
	}

	content, err := b.Render()
	require.NoError(t, err)

	assert.Contains(t, content, "#!/bin/sh")
	assert.Contains(t, content, "set -e")
	assert.Contains(t, content, "--to-port 8443")
	assert.Contains(t, content, "exec /usr/local/bin/gateway")
}

func TestEntrypointBuilder_AgentWithGateway(t *testing.T) {
	b := &EntrypointBuilder{
		Variant:           "agent",
		HasGateway:        true,
		GatewayListenPort: 8443,
		User:              "agent",
		RuntimeCmd:        "sleep infinity",
	}

	content, err := b.Render()
	require.NoError(t, err)

	assert.Contains(t, content, "ip route replace default")
	assert.Contains(t, content, "getent hosts $GATEWAY_HOST")
	assert.Contains(t, content, "nameserver $GATEWAY_IP")
	assert.Contains(t, content, "/etc/resolv.conf")
	assert.Contains(t, content, "redirecting traffic to gateway proxy")
	assert.Contains(t, content, "iptables -t nat -A OUTPUT -p tcp --dport 443 -j DNAT --to-destination $GATEWAY_IP:8443")
	assert.NotContains(t, content, "/usr/local/bin/gateway")
	assert.Contains(t, content, "exec su -c '")
	assert.Contains(t, content, "exec sleep infinity' agent")
}

func TestEntrypointBuilder_AgentWithChannelManager(t *testing.T) {
	b := &EntrypointBuilder{
		Variant:           "agent",
		HasGateway:        true,
		HasMITM:           true,
		GatewayListenPort: 8443,
		User:              "agent",
		ChannelManager:    true,
		CMEntryPoint:      "node /opt/channel-manager/dist/index.js",
		CACertPath:        sandboxCACertPath,
	}

	content, err := b.Render()
	require.NoError(t, err)

	assert.Contains(t, content, "nameserver $GATEWAY_IP")
	assert.Contains(t, content, "exec su -c '")
	assert.Contains(t, content, "exec node /opt/channel-manager/dist/index.js' agent")
	assert.Contains(t, content, "waiting for sandbox CA certificate")
	assert.Contains(t, content, "update-ca-certificates")
}

func TestEntrypointBuilder_AgentWithHooks(t *testing.T) {
	b := &EntrypointBuilder{
		Variant:    "agent",
		HasHooks:   true,
		Hooks:      []string{"setup.sh"},
		User:       "agent",
		RuntimeCmd: "sleep infinity",
	}

	content, err := b.Render()
	require.NoError(t, err)

	assert.Contains(t, content, "/opt/hooks/setup.sh")
	assert.Contains(t, content, "exec su -c '")
	assert.Contains(t, content, "exec sleep infinity' agent")
}

func TestEntrypointBuilder_AgentWithHomeOverride(t *testing.T) {
	b := &EntrypointBuilder{
		Variant:         "agent",
		HasHomeOverride: true,
		User:            "agent",
		RuntimeCmd:      "sleep infinity",
	}

	content, err := b.Render()
	require.NoError(t, err)

	assert.Contains(t, content, "cp -rT /opt/home-override /home/agent")
}

func TestEntrypointBuilder_AgentWithPorts(t *testing.T) {
	b := &EntrypointBuilder{
		Variant:           "agent",
		HasGateway:        true,
		GatewayListenPort: 8443,
		Ports: []PortMapping{
			{HostPort: "1455", ContainerPort: "1455"},
			{HostPort: "8080", ContainerPort: "3000"},
		},
		User:       "agent",
		RuntimeCmd: "sleep infinity",
	}

	content, err := b.Render()
	require.NoError(t, err)

	assert.Contains(t, content, "--dport 1455 -j DNAT --to-destination 127.0.0.1:1455")
	assert.Contains(t, content, "--dport 3000 -j DNAT --to-destination 127.0.0.1:3000")
}

func TestEntrypointBuilder_AgentWithHTTPPorts(t *testing.T) {
	b := &EntrypointBuilder{
		Variant:           "agent",
		HasGateway:        true,
		GatewayListenPort: 8443,
		HTTPPorts:         []string{"8765", "9090"},
		User:              "agent",
		RuntimeCmd:        "sleep infinity",
	}

	content, err := b.Render()
	require.NoError(t, err)

	assert.Contains(t, content, "iptables -t nat -A OUTPUT -p tcp --dport 443 -j DNAT --to-destination $GATEWAY_IP:8443")
	assert.Contains(t, content, "iptables -t nat -A OUTPUT -p tcp --dport 8765 -j DNAT --to-destination $GATEWAY_IP:8443")
	assert.Contains(t, content, "iptables -t nat -A OUTPUT -p tcp --dport 9090 -j DNAT --to-destination $GATEWAY_IP:8443")
}

func TestEntrypointBuilder_UserCommandFormat(t *testing.T) {
	t.Run("hooks then runtime", func(t *testing.T) {
		b := &EntrypointBuilder{
			Variant:    "agent",
			HasHooks:   true,
			Hooks:      []string{"setup.sh"},
			User:       "agent",
			RuntimeCmd: "sleep infinity",
		}
		b.buildUserCommand()

		expected := `echo "entrypoint: running hooks..." && /opt/hooks/setup.sh && echo "entrypoint: starting agent..." && exec sleep infinity`
		assert.Equal(t, expected, b.UserCommand)
	})

	t.Run("channel-manager no hooks", func(t *testing.T) {
		b := &EntrypointBuilder{
			Variant:        "agent",
			User:           "agent",
			ChannelManager: true,
			CMEntryPoint:   "node /opt/channel-manager/dist/index.js",
		}
		b.buildUserCommand()

		expected := `echo "entrypoint: starting channel-manager..." && exec node /opt/channel-manager/dist/index.js`
		assert.Equal(t, expected, b.UserCommand)
	})

	t.Run("multiple hooks", func(t *testing.T) {
		b := &EntrypointBuilder{
			Variant:    "agent",
			HasHooks:   true,
			Hooks:      []string{"first.sh", "second.sh"},
			User:       "agent",
			RuntimeCmd: "sleep infinity",
		}
		b.buildUserCommand()

		assert.Contains(t, b.UserCommand, "/opt/hooks/first.sh && /opt/hooks/second.sh")
	})
}
