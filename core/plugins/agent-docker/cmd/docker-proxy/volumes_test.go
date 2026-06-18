package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSupportsVolumeSubpath(t *testing.T) {
	tests := []struct {
		apiVersion string
		expected   bool
	}{
		{"1.45", true},
		{"1.46", true},
		{"1.44", false},
		{"1.40", false},
		{"2.0", true},
		{"", false},
	}
	for _, tt := range tests {
		v := &DockerVersion{APIVersion: tt.apiVersion}
		assert.Equal(t, tt.expected, v.SupportsVolumeSubpath(), "API %s", tt.apiVersion)
	}
}

func TestVolumeTranslator_TranslateBinds_MatchesSubpath(t *testing.T) {
	vt := &VolumeTranslator{
		supportsSubpath: true,
		mounts: []VolumeMount{
			{ContainerPath: "/home/agent", VolumeName: "agent-home"},
			{ContainerPath: "/nix", VolumeName: "agent-nix"},
		},
	}

	remaining, mounts, err := vt.TranslateBinds([]string{
		"/home/agent/workspace/project/src:/app",
	})
	assert.NoError(t, err)
	assert.Empty(t, remaining)
	assert.Len(t, mounts, 1)

	m := mounts[0]
	assert.Equal(t, "volume", m["Type"])
	assert.Equal(t, "agent-home", m["Source"])
	assert.Equal(t, "/app", m["Target"])
	opts, _ := m["VolumeOptions"].(map[string]any)
	assert.Equal(t, "workspace/project/src", opts["Subpath"])
}

func TestVolumeTranslator_TranslateBinds_ExactPathMatch(t *testing.T) {
	vt := &VolumeTranslator{
		supportsSubpath: true,
		mounts: []VolumeMount{
			{ContainerPath: "/home/agent", VolumeName: "agent-home"},
		},
	}

	remaining, mounts, err := vt.TranslateBinds([]string{
		"/home/agent:/data",
	})
	assert.NoError(t, err)
	assert.Empty(t, remaining)
	assert.Len(t, mounts, 1)

	m := mounts[0]
	assert.Equal(t, "agent-home", m["Source"])
	assert.Equal(t, "/data", m["Target"])
	// No subpath for exact match
	_, hasOpts := m["VolumeOptions"]
	assert.False(t, hasOpts)
}

func TestVolumeTranslator_TranslateBinds_NamedVolumePassthrough(t *testing.T) {
	vt := &VolumeTranslator{
		supportsSubpath: true,
		mounts: []VolumeMount{
			{ContainerPath: "/home/agent", VolumeName: "agent-home"},
		},
	}

	remaining, mounts, err := vt.TranslateBinds([]string{
		"my-data-vol:/data",
		"/home/agent/workspace:/app",
	})
	assert.NoError(t, err)
	assert.Equal(t, []string{"my-data-vol:/data"}, remaining)
	assert.Len(t, mounts, 1)
}

func TestVolumeTranslator_TranslateBinds_ReadOnly(t *testing.T) {
	vt := &VolumeTranslator{
		supportsSubpath: true,
		mounts: []VolumeMount{
			{ContainerPath: "/home/agent", VolumeName: "agent-home"},
		},
	}

	_, mounts, err := vt.TranslateBinds([]string{
		"/home/agent/config.yaml:/etc/app/config.yaml:ro",
	})
	assert.NoError(t, err)
	assert.Len(t, mounts, 1)
	assert.Equal(t, true, mounts[0]["ReadOnly"])
}

func TestVolumeTranslator_TranslateBinds_UnknownPath_Error(t *testing.T) {
	vt := &VolumeTranslator{
		supportsSubpath: true,
		dockerVersion:   "27.1.1",
		mounts: []VolumeMount{
			{ContainerPath: "/home/agent", VolumeName: "agent-home"},
		},
	}

	_, _, err := vt.TranslateBinds([]string{
		"/etc/shadow:/etc/shadow",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not under any shared volume")
}

func TestVolumeTranslator_TranslateBinds_OldDocker_Error(t *testing.T) {
	vt := &VolumeTranslator{
		supportsSubpath: false,
		dockerVersion:   "25.0.3",
		mounts:          nil,
	}

	_, _, err := vt.TranslateBinds([]string{
		"/home/agent/workspace:/app",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Docker Engine 26+")
	assert.Contains(t, err.Error(), "25.0.3")
}

func TestVolumeTranslator_LongerPrefixMatchesFirst(t *testing.T) {
	vt := &VolumeTranslator{
		supportsSubpath: true,
		mounts: []VolumeMount{
			// Longer prefix first (sorted by discoverAgentMounts)
			{ContainerPath: "/home/agent/workspace", VolumeName: "workspace-vol"},
			{ContainerPath: "/home/agent", VolumeName: "home-vol"},
		},
	}

	_, mounts, err := vt.TranslateBinds([]string{
		"/home/agent/workspace/project:/app",
	})
	assert.NoError(t, err)
	assert.Len(t, mounts, 1)
	// Should match workspace-vol (longer prefix), not home-vol
	assert.Equal(t, "workspace-vol", mounts[0]["Source"])
	opts, _ := mounts[0]["VolumeOptions"].(map[string]any)
	assert.Equal(t, "project", opts["Subpath"])
}

func TestTranslateBindMounts_ModifiesBody(t *testing.T) {
	dp := &DockerProxy{
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
				"/home/agent/workspace/src:/app",
				"cache-vol:/cache",
			},
		},
	}

	err := dp.translateBindMounts(body)
	assert.NoError(t, err)

	// Host bind should be removed, named volume stays
	hc, _ := body["HostConfig"].(map[string]any)
	binds, _ := hc["Binds"].([]any)
	assert.Equal(t, []any{"cache-vol:/cache"}, binds)

	// Translated mount should be in body["Mounts"]
	mounts, _ := body["Mounts"].([]any)
	assert.Len(t, mounts, 1)
	m, _ := mounts[0].(map[string]any)
	assert.Equal(t, "volume", m["Type"])
	assert.Equal(t, "agent-home", m["Source"])
	assert.Equal(t, "/app", m["Target"])
}
