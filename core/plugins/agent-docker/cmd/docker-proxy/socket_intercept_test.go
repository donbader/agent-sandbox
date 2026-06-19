package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTranslateBindMounts_DockerSocket_StandardPath(t *testing.T) {
	dp := &DockerProxy{
		cfg: &ProxyConfig{
			AgentName:   "coder",
			SandboxID:   "my-project-coder",
			NetworkName: "my-project_sandbox",
		},
		volumes: &VolumeTranslator{
			supportsSubpath: true,
			mounts: []VolumeMount{
				{ContainerPath: "/home/agent", VolumeName: "agent-home"},
			},
		},
	}

	body := map[string]any{
		"Image": "docker:dind",
		"HostConfig": map[string]any{
			"Binds": []any{
				"/var/run/docker.sock:/var/run/docker.sock",
			},
		},
	}

	err := dp.translateBindMounts(body)
	require.NoError(t, err)

	// Socket bind should be removed
	hc, _ := body["HostConfig"].(map[string]any)
	_, hasBind := hc["Binds"]
	assert.False(t, hasBind, "docker.sock bind should be removed")

	// DOCKER_HOST env should be injected
	env, _ := body["Env"].([]any)
	assert.Contains(t, env, "DOCKER_HOST=tcp://coder-agent-docker-proxy:2375")
}

func TestTranslateBindMounts_DockerSocket_AlternativeSrcPath(t *testing.T) {
	dp := &DockerProxy{
		cfg: &ProxyConfig{
			AgentName:   "dev",
			SandboxID:   "sandbox-dev",
			NetworkName: "sandbox_net",
		},
		volumes: &VolumeTranslator{
			supportsSubpath: true,
			mounts:          []VolumeMount{},
		},
	}

	body := map[string]any{
		"Image": "node:20",
		"HostConfig": map[string]any{
			"Binds": []any{
				"/run/docker.sock:/var/run/docker.sock",
			},
		},
	}

	err := dp.translateBindMounts(body)
	require.NoError(t, err)

	hc, _ := body["HostConfig"].(map[string]any)
	_, hasBind := hc["Binds"]
	assert.False(t, hasBind)

	env, _ := body["Env"].([]any)
	assert.Contains(t, env, "DOCKER_HOST=tcp://dev-agent-docker-proxy:2375")
}

func TestTranslateBindMounts_DockerSocket_AlternativeTargetPath(t *testing.T) {
	dp := &DockerProxy{
		cfg: &ProxyConfig{
			AgentName:   "coder",
			SandboxID:   "test",
			NetworkName: "sandbox",
		},
		volumes: &VolumeTranslator{
			supportsSubpath: true,
			mounts:          []VolumeMount{},
		},
	}

	// Some tools mount socket to /run/docker.sock or custom target
	body := map[string]any{
		"Image": "buildkitd:latest",
		"HostConfig": map[string]any{
			"Binds": []any{
				"/var/run/docker.sock:/run/docker.sock",
			},
		},
	}

	err := dp.translateBindMounts(body)
	require.NoError(t, err)

	hc, _ := body["HostConfig"].(map[string]any)
	_, hasBind := hc["Binds"]
	assert.False(t, hasBind)

	env, _ := body["Env"].([]any)
	assert.Contains(t, env, "DOCKER_HOST=tcp://coder-agent-docker-proxy:2375")
}

func TestTranslateBindMounts_DockerSocket_MixedWithOtherBinds(t *testing.T) {
	dp := &DockerProxy{
		cfg: &ProxyConfig{
			AgentName:   "coder",
			SandboxID:   "test",
			NetworkName: "sandbox",
		},
		volumes: &VolumeTranslator{
			supportsSubpath: true,
			mounts: []VolumeMount{
				{ContainerPath: "/home/agent", VolumeName: "agent-home"},
			},
		},
	}

	body := map[string]any{
		"Image": "docker:dind",
		"HostConfig": map[string]any{
			"Binds": []any{
				"/var/run/docker.sock:/var/run/docker.sock",
				"/home/agent/workspace:/app",
				"cache-vol:/cache",
			},
		},
	}

	err := dp.translateBindMounts(body)
	require.NoError(t, err)

	// Named volume bind stays, socket removed, host path translated
	hc, _ := body["HostConfig"].(map[string]any)
	binds, _ := hc["Binds"].([]any)
	assert.Equal(t, []any{"cache-vol:/cache"}, binds)

	// Volume-subpath mount for workspace
	mounts, _ := body["Mounts"].([]any)
	assert.Len(t, mounts, 1)

	// DOCKER_HOST injected
	env, _ := body["Env"].([]any)
	assert.Contains(t, env, "DOCKER_HOST=tcp://coder-agent-docker-proxy:2375")
}

func TestTranslateBindMounts_DockerSocket_PreservesExistingEnv(t *testing.T) {
	dp := &DockerProxy{
		cfg: &ProxyConfig{
			AgentName:   "coder",
			SandboxID:   "test",
			NetworkName: "sandbox",
		},
		volumes: &VolumeTranslator{
			supportsSubpath: true,
			mounts:          []VolumeMount{},
		},
	}

	body := map[string]any{
		"Image": "docker:dind",
		"Env":   []any{"FOO=bar", "PATH=/usr/bin"},
		"HostConfig": map[string]any{
			"Binds": []any{
				"/var/run/docker.sock:/var/run/docker.sock",
			},
		},
	}

	err := dp.translateBindMounts(body)
	require.NoError(t, err)

	env, _ := body["Env"].([]any)
	assert.Contains(t, env, "FOO=bar")
	assert.Contains(t, env, "PATH=/usr/bin")
	assert.Contains(t, env, "DOCKER_HOST=tcp://coder-agent-docker-proxy:2375")
}

func TestTranslateBindMounts_DockerSocket_OverridesExistingDockerHost(t *testing.T) {
	dp := &DockerProxy{
		cfg: &ProxyConfig{
			AgentName:   "coder",
			SandboxID:   "test",
			NetworkName: "sandbox",
		},
		volumes: &VolumeTranslator{
			supportsSubpath: true,
			mounts:          []VolumeMount{},
		},
	}

	// Container already has DOCKER_HOST set — we override it to point to our proxy
	body := map[string]any{
		"Image": "docker:dind",
		"Env":   []any{"DOCKER_HOST=tcp://some-other:1234", "FOO=bar"},
		"HostConfig": map[string]any{
			"Binds": []any{
				"/var/run/docker.sock:/var/run/docker.sock",
			},
		},
	}

	err := dp.translateBindMounts(body)
	require.NoError(t, err)

	env, _ := body["Env"].([]any)
	// Old DOCKER_HOST should be replaced
	assert.NotContains(t, env, "DOCKER_HOST=tcp://some-other:1234")
	assert.Contains(t, env, "DOCKER_HOST=tcp://coder-agent-docker-proxy:2375")
	assert.Contains(t, env, "FOO=bar")
}

func TestTranslateBindMounts_DockerSocket_ReadOnlyVariant(t *testing.T) {
	dp := &DockerProxy{
		cfg: &ProxyConfig{
			AgentName:   "coder",
			SandboxID:   "test",
			NetworkName: "sandbox",
		},
		volumes: &VolumeTranslator{
			supportsSubpath: true,
			mounts:          []VolumeMount{},
		},
	}

	// Some mount docker.sock with :ro
	body := map[string]any{
		"Image": "portainer:latest",
		"HostConfig": map[string]any{
			"Binds": []any{
				"/var/run/docker.sock:/var/run/docker.sock:ro",
			},
		},
	}

	err := dp.translateBindMounts(body)
	require.NoError(t, err)

	hc, _ := body["HostConfig"].(map[string]any)
	_, hasBind := hc["Binds"]
	assert.False(t, hasBind)

	env, _ := body["Env"].([]any)
	assert.Contains(t, env, "DOCKER_HOST=tcp://coder-agent-docker-proxy:2375")
}

func TestTranslateBindMounts_NoSocket_NoDockerHostInjected(t *testing.T) {
	dp := &DockerProxy{
		cfg: &ProxyConfig{
			AgentName:   "coder",
			SandboxID:   "test",
			NetworkName: "sandbox",
		},
		volumes: &VolumeTranslator{
			supportsSubpath: true,
			mounts: []VolumeMount{
				{ContainerPath: "/home/agent", VolumeName: "agent-home"},
			},
		},
	}

	body := map[string]any{
		"Image": "node:20",
		"HostConfig": map[string]any{
			"Binds": []any{
				"/home/agent/workspace:/app",
			},
		},
	}

	err := dp.translateBindMounts(body)
	require.NoError(t, err)

	// No DOCKER_HOST should be injected when no socket mount
	env, _ := body["Env"].([]any)
	for _, e := range env {
		s, _ := e.(string)
		assert.NotContains(t, s, "DOCKER_HOST")
	}
}
