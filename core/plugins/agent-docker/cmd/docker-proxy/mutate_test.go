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
		SandboxID:    "sandbox-001",
		AgentName:    "agent",
		NetworkName:  "sandbox_net",
		AllowCompose: true,
		MemoryBytes:  1024,
		NanoCPUs:     1000,
		PidsLimit:    100,
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

	// Should set SANDBOX_GATEWAY_HOST env
	env, _ := body["Env"].([]any)
	assert.Contains(t, env, "SANDBOX_GATEWAY_HOST=agent-gateway")

	// Should wrap entrypoint with init script
	ep, _ := body["Entrypoint"].([]any)
	assert.Equal(t, "/bin/sh", ep[0])
	assert.Equal(t, "-c", ep[1])
	assert.Contains(t, ep[2], "exec \"$@\"")
	assert.Equal(t, "--", ep[3])

	// Original entrypoint + cmd preserved in Cmd
	cmd, _ := body["Cmd"].([]any)
	assert.Equal(t, []any{"docker-entrypoint.sh", "postgres"}, cmd)
}

func TestMutateCreate_ComposeMode_WithNetworks(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:    "sandbox-001",
		AgentName:    "agent",
		NetworkName:  "sandbox_net",
		AllowCompose: true,
		MemoryBytes:  1024,
		NanoCPUs:     1000,
		PidsLimit:    100,
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

	// Should NOT inject sandbox network (compose has its own)
	hc, _ := body["HostConfig"].(map[string]any)
	assert.Equal(t, "myapp_default", hc["NetworkMode"])
	nc, _ := body["NetworkingConfig"].(map[string]any)
	ec, _ := nc["EndpointsConfig"].(map[string]any)
	_, hasSandbox := ec["sandbox_net"]
	assert.False(t, hasSandbox)

	// Should still inject init wrapper
	capAdd, _ := hc["CapAdd"].([]any)
	assert.Contains(t, capAdd, "NET_ADMIN")

	ep, _ := body["Entrypoint"].([]any)
	assert.Equal(t, "/bin/sh", ep[0])
	assert.Contains(t, ep[2], "iptables")
}

func TestInjectInitWrapper_NoOriginalCmd(t *testing.T) {
	cfg := &ProxyConfig{
		AgentName: "myagent",
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

	// Cmd should be deleted (let Docker use image default)
	_, hasCmd := body["Cmd"]
	assert.False(t, hasCmd)

	// Should add NET_ADMIN
	capAdd, _ := hc["CapAdd"].([]any)
	assert.Contains(t, capAdd, "NET_ADMIN")

	// Should add env
	env, _ := body["Env"].([]any)
	assert.Contains(t, env, "SANDBOX_GATEWAY_HOST=myagent-gateway")
}

func TestInjectInitWrapper_WithEntrypointAndCmd(t *testing.T) {
	cfg := &ProxyConfig{
		AgentName: "myagent",
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
}
