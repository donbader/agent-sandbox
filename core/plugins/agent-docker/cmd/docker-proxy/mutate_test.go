package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMutateCreate(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:   "my-project-coder",
		AgentName:   "coder",
		NetworkName: "my-project_sandbox",
		NetworkID:   "my-project_sandbox",
		MemoryBytes: 2 * 1024 * 1024 * 1024,
		NanoCPUs:    2000000000,
		PidsLimit:   256,
	}
	m := NewMutator(cfg)

	body := map[string]any{
		"Image": "node:20",
		"HostConfig": map[string]any{
			"NetworkMode": "bridge",
		},
	}

	m.MutateCreate(body, "my-app")

	// Check labels
	labels, _ := body["Labels"].(map[string]any)
	assert.Equal(t, "coder", labels["agent-sandbox.agent"])
	assert.Equal(t, "my-project-coder", labels["agent-sandbox.sandbox"])

	// Check host config
	hc, _ := body["HostConfig"].(map[string]any)
	assert.Equal(t, "my-project_sandbox", hc["NetworkMode"])
	assert.Equal(t, int64(2*1024*1024*1024), hc["Memory"])
	assert.Equal(t, int64(2000000000), hc["NanoCpus"])
	assert.Equal(t, int64(256), hc["PidsLimit"])
	rp, _ := hc["RestartPolicy"].(map[string]any)
	assert.Equal(t, "no", rp["Name"])
}

func TestMutateCreate_NamespaceContainerName(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:   "my-project-coder",
		AgentName:   "coder",
		NetworkName: "my-project_sandbox",
		NetworkID:   "my-project_sandbox",
		MemoryBytes: 2 * 1024 * 1024 * 1024,
		NanoCPUs:    2000000000,
		PidsLimit:   256,
	}
	m := NewMutator(cfg)

	// With user-provided name
	name := m.NamespaceContainerName("my-postgres")
	assert.Equal(t, "my-project-coder-my-postgres", name)

	// Empty name gets random suffix
	name = m.NamespaceContainerName("")
	assert.True(t, len(name) > len("my-project-coder-"))
	assert.Contains(t, name, "my-project-coder-")
}

func TestMutateCreate_ComposeMode_StandaloneRun(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:          "sandbox-001",
		AgentName:          "agent",
		NetworkName:        "sandbox_net",
		NetworkID:          "sandbox_net",
		AllowCompose:       true,
		MemoryBytes:        1024,
		NanoCPUs:           1000,
		PidsLimit:          100,
		GatewayIP:          "172.18.0.2",
		GatewayRouteScript: "#!/bin/sh\nGATEWAY_IP=\"172.18.0.2\"\nip route replace default via $GATEWAY_IP",
	}
	m := NewMutator(cfg)

	// Standalone docker run: no networks specified
	body := map[string]any{
		"Image":      "postgres:16",
		"Cmd":        []any{"postgres"},
		"Entrypoint": []any{"docker-entrypoint.sh"},
	}

	m.MutateCreate(body, "my-pg")

	// Should inject sandbox network (standalone)
	hc, _ := body["HostConfig"].(map[string]any)
	assert.Equal(t, "sandbox_net", hc["NetworkMode"])
	nc, _ := body["NetworkingConfig"].(map[string]any)
	ec, _ := nc["EndpointsConfig"].(map[string]any)
	_, hasSandbox := ec["sandbox_net"]
	assert.True(t, hasSandbox)

	// Should inject init wrapper
	capAdd, _ := hc["CapAdd"].([]any)
	assert.Contains(t, capAdd, "NET_ADMIN")

	// Should set DNS to gateway IP
	dns, _ := hc["Dns"].([]string)
	assert.Equal(t, []string{"172.18.0.2"}, dns)

	// Should NOT set SANDBOX_GATEWAY_HOST env (no longer needed)
	env, _ := body["Env"].([]any)
	for _, e := range env {
		if s, ok := e.(string); ok {
			assert.NotContains(t, s, "SANDBOX_GATEWAY_HOST")
		}
	}

	// Should mount certs volume (compose volume name: {project}_{agentName}-certs)
	mounts, _ := hc["Mounts"].([]any)
	assert.Len(t, mounts, 1)
	mount, _ := mounts[0].(map[string]any)
	assert.Equal(t, "volume", mount["Type"])
	assert.Equal(t, "sandbox_net_agent-certs", mount["Source"])
	assert.Equal(t, "/shared/certs", mount["Target"])
	assert.Equal(t, true, mount["ReadOnly"])

	// Should wrap entrypoint with init script
	ep, _ := body["Entrypoint"].([]any)
	assert.Equal(t, "/bin/sh", ep[0])
	assert.Equal(t, "-c", ep[1])
	assert.Contains(t, ep[2], "exec \"$@\"")
	assert.Contains(t, ep[2], "/shared/certs/gateway-route.sh")
	assert.Equal(t, "--", ep[3])

	// Original entrypoint + cmd preserved in Cmd
	cmd, _ := body["Cmd"].([]any)
	assert.Equal(t, []any{"docker-entrypoint.sh", "postgres"}, cmd)
}

func TestMutateCreate_ComposeMode_WithNetworks(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:          "sandbox-001",
		AgentName:          "agent",
		NetworkName:        "sandbox_net",
		NetworkID:          "sandbox_net",
		AllowCompose:       true,
		MemoryBytes:        1024,
		NanoCPUs:           1000,
		PidsLimit:          100,
		GatewayIP:          "172.18.0.2",
		GatewayRouteScript: "#!/bin/sh\nGATEWAY_IP=\"172.18.0.2\"\nip route replace default via $GATEWAY_IP",
	}
	m := NewMutator(cfg)

	// Compose container: has its own network
	body := map[string]any{
		"Image": "redis:7",
		"Cmd":   []any{"redis-server"},
		"NetworkingConfig": map[string]any{
			"EndpointsConfig": map[string]any{
				"myapp_default": map[string]any{},
			},
		},
		"HostConfig": map[string]any{
			"NetworkMode": "myapp_default",
		},
	}

	m.MutateCreate(body, "my-redis")

	// Should inject sandbox network alongside compose network (for gateway reachability)
	hc, _ := body["HostConfig"].(map[string]any)
	assert.Equal(t, "myapp_default", hc["NetworkMode"])
	nc, _ := body["NetworkingConfig"].(map[string]any)
	ec, _ := nc["EndpointsConfig"].(map[string]any)
	_, hasSandbox := ec["sandbox_net"]
	assert.True(t, hasSandbox)

	// Should still inject init wrapper
	capAdd, _ := hc["CapAdd"].([]any)
	assert.Contains(t, capAdd, "NET_ADMIN")

	// Should set DNS to gateway IP
	dns, _ := hc["Dns"].([]string)
	assert.Equal(t, []string{"172.18.0.2"}, dns)

	// Should mount certs volume
	mounts, _ := hc["Mounts"].([]any)
	assert.Len(t, mounts, 1)

	ep, _ := body["Entrypoint"].([]any)
	assert.Equal(t, "/bin/sh", ep[0])
	assert.Contains(t, ep[2], "/shared/certs/gateway-route.sh")
}

func TestInjectInitWrapper_NoOriginalCmd(t *testing.T) {
	cfg := &ProxyConfig{
		AgentName:          "myagent",
		NetworkName:        "myproject_sandbox",
		NetworkID:          "myproject_sandbox",
		GatewayIP:          "172.18.0.5",
		GatewayRouteScript: "#!/bin/sh\nGATEWAY_IP=\"172.18.0.5\"\nip route replace default via $GATEWAY_IP",
	}
	m := NewMutator(cfg)

	body := map[string]any{
		"Image": "nginx",
	}
	hc := map[string]any{}

	m.injectInitWrapper(body, hc)

	// Should set entrypoint to init wrapper
	ep, _ := body["Entrypoint"].([]any)
	assert.Equal(t, "/bin/sh", ep[0])
	assert.Equal(t, "--", ep[3])

	// Init script should contain the gateway route script content
	initScript, _ := ep[2].(string)
	assert.Contains(t, initScript, "/shared/certs/gateway-route.sh")
	assert.Contains(t, initScript, "exec \"$@\"")

	// Cmd should be deleted (let Docker use image default)
	_, hasCmd := body["Cmd"]
	assert.False(t, hasCmd)

	// Should add NET_ADMIN
	capAdd, _ := hc["CapAdd"].([]any)
	assert.Contains(t, capAdd, "NET_ADMIN")

	// Should set DNS
	dns, _ := hc["Dns"].([]string)
	assert.Equal(t, []string{"172.18.0.5"}, dns)

	// Should mount certs volume
	mounts, _ := hc["Mounts"].([]any)
	assert.Len(t, mounts, 1)
	mount, _ := mounts[0].(map[string]any)
	assert.Equal(t, "myproject_myagent-certs", mount["Source"])
	assert.Equal(t, "/shared/certs", mount["Target"])

	// Should NOT add SANDBOX_GATEWAY_HOST env
	_, hasEnv := body["Env"]
	assert.False(t, hasEnv)
}

func TestInjectInitWrapper_WithEntrypointAndCmd(t *testing.T) {
	cfg := &ProxyConfig{
		AgentName:          "myagent",
		NetworkName:        "myproject_sandbox",
		NetworkID:          "myproject_sandbox",
		GatewayIP:          "172.18.0.5",
		GatewayRouteScript: "#!/bin/sh\nGATEWAY_IP=\"172.18.0.5\"\nip route replace default via $GATEWAY_IP",
	}
	m := NewMutator(cfg)

	body := map[string]any{
		"Image":      "custom",
		"Entrypoint": []any{"/usr/bin/tini", "--"},
		"Cmd":        []any{"node", "server.js"},
	}
	hc := map[string]any{}

	m.injectInitWrapper(body, hc)

	// Original entrypoint + cmd merged into new Cmd
	cmd, _ := body["Cmd"].([]any)
	assert.Equal(t, []any{"/usr/bin/tini", "--", "node", "server.js"}, cmd)

	// New entrypoint is init wrapper
	ep, _ := body["Entrypoint"].([]any)
	assert.Equal(t, "/bin/sh", ep[0])
	initScript, _ := ep[2].(string)
	assert.Contains(t, initScript, "exec \"$@\"")
	assert.Contains(t, initScript, "/shared/certs/gateway-route.sh")
}
