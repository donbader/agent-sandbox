package plugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderContributions(t *testing.T) {
	raw := `
name: github-pat
options:
  token:
    type: string
    required: true
contributes:
  runtime:
    extra_builds:
      - "RUN echo {{ .plugin.options.token }}"
  gateway:
    egress:
      - hosts:
          - https://github.com
        headers:
          Authorization: "Bearer {{ .plugin.options.token }}"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{"token": "ghp_abc123"}
	rendered, err := RenderContributions(p, opts, RenderContext{Self: map[string]any{"name": "test-agent"}})
	require.NoError(t, err)

	assert.Equal(t, "RUN echo ghp_abc123", rendered.Runtime.ExtraBuilds[0])
	assert.Equal(t, "Bearer ghp_abc123", rendered.Gateway.Egress[0].Headers["Authorization"])
}

func TestRenderContributions_MissingRequired(t *testing.T) {
	raw := `
name: test
options:
  token:
    type: string
    required: true
contributes:
  runtime:
    extra_builds: []
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{}
	_, err = RenderContributions(p, opts, RenderContext{Self: map[string]any{"name": "test-agent"}})
	assert.ErrorContains(t, err, "required option \"token\" not provided")
}

func TestRenderContributions_DefaultValues(t *testing.T) {
	raw := `
name: test
options:
  version:
    type: string
    default: "1.0.0"
contributes:
  runtime:
    extra_builds:
      - "RUN install v{{ .plugin.options.version }}"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{}
	rendered, err := RenderContributions(p, opts, RenderContext{Self: map[string]any{"name": "test-agent"}})
	require.NoError(t, err)

	assert.Equal(t, "RUN install v1.0.0", rendered.Runtime.ExtraBuilds[0])
}

func TestRenderContributions_PreEntrypointAndPorts(t *testing.T) {
	raw := `
name: ssh
options:
  port:
    type: integer
    default: 2222
contributes:
  runtime:
    extra_builds:
      - "RUN apt-get install -y openssh-server"
    pre_entrypoint:
      - "/usr/sbin/sshd -p {{ .plugin.options.port }}"
    ports:
      - "{{ .plugin.options.port }}:{{ .plugin.options.port }}"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{}
	rendered, err := RenderContributions(p, opts, RenderContext{Self: map[string]any{"name": "test-agent"}})
	require.NoError(t, err)

	require.Len(t, rendered.Runtime.PreEntrypoint, 1)
	assert.Equal(t, "/usr/sbin/sshd -p 2222", rendered.Runtime.PreEntrypoint[0])
	require.Len(t, rendered.Runtime.Ports, 1)
	assert.Equal(t, "2222:2222", rendered.Runtime.Ports[0])
}

func TestRenderContributions_PathTraversal(t *testing.T) {
	raw := `
name: mcp-oauth
options:
  token_dir:
    type: string
    required: false
    default: "/data/oauth-tokens"
contributes:
  gateway:
    volumes:
      - "oauth-tokens:{{ .plugin.options.token_dir }}"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{"token_dir": "../../etc/evil"}
	_, err = RenderContributions(p, opts, RenderContext{Self: map[string]any{"name": "test-agent"}})
	assert.ErrorContains(t, err, "path traversal")
}

func TestRenderContributions_PreEntrypointCustomPort(t *testing.T) {
	raw := `
name: ssh
options:
  port:
    type: integer
    default: 2222
contributes:
  runtime:
    pre_entrypoint:
      - "/usr/sbin/sshd -p {{ .plugin.options.port }}"
    ports:
      - "{{ .plugin.options.port }}:{{ .plugin.options.port }}"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{"port": 8022}
	rendered, err := RenderContributions(p, opts, RenderContext{Self: map[string]any{"name": "test-agent"}})
	require.NoError(t, err)

	assert.Equal(t, "/usr/sbin/sshd -p 8022", rendered.Runtime.PreEntrypoint[0])
	assert.Equal(t, "8022:8022", rendered.Runtime.Ports[0])
}

func TestRenderContributions_UnknownFieldError(t *testing.T) {
	raw := `
name: my-plugin
contributes:
  runtime:
    extra_builds:
      - "RUN echo hello"
    entrypoint: ["my-binary"]
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{}
	_, err = RenderContributions(p, opts, RenderContext{Self: map[string]any{"name": "test-agent"}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "my-plugin")
}

func TestRenderContributions_UnknownTopLevelField(t *testing.T) {
	raw := `
name: my-plugin
contributes:
  runtime:
    extra_builds:
      - "RUN echo hello"
  bogus_section:
    foo: bar
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{}
	_, err = RenderContributions(p, opts, RenderContext{Self: map[string]any{"name": "test-agent"}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "my-plugin")
}

func TestRenderContributions_Functions(t *testing.T) {
	raw := `
name: version-plugin
functions:
  gitHash:
    script: "./scripts/git-hash.sh"
options:
  env_key:
    type: string
    default: "GIT_HASH"
contributes:
  runtime:
    environment:
      "{{ .plugin.options.env_key }}": "{{ call .plugin.gitHash }}"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	contribs, err := RenderContributions(p, map[string]any{}, RenderContext{
		Self:      map[string]any{"name": "test-agent"},
		Generator: map[string]any{"core_version": "v1.0.0"},
		Functions: map[string]string{"gitHash": "abc1234"},
	})
	require.NoError(t, err)
	assert.Equal(t, "abc1234", contribs.Runtime.Environment["GIT_HASH"])
}

func TestRenderContributions_Functions_NotComputed(t *testing.T) {
	raw := `
name: broken-plugin
functions:
  missingFunc:
    script: "./scripts/missing.sh"
contributes:
  runtime:
    environment:
      FOO: "{{ call .plugin.missingFunc }}"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	// Functions map doesn't include "missingFunc" — should error
	_, err = RenderContributions(p, map[string]any{}, RenderContext{
		Self:      map[string]any{"name": "test-agent"},
		Generator: map[string]any{},
		Functions: map[string]string{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missingFunc")
}

func TestRenderContributions_GeneratorContext(t *testing.T) {
	raw := `
name: version-plugin
contributes:
  runtime:
    environment:
      CORE_VERSION: "{{ .generator.core_version }}"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	contribs, err := RenderContributions(p, map[string]any{}, RenderContext{
		Self:      map[string]any{"name": "test-agent"},
		Generator: map[string]any{"core_version": "v1.44.0"},
		Functions: map[string]string{},
	})
	require.NoError(t, err)
	assert.Equal(t, "v1.44.0", contribs.Runtime.Environment["CORE_VERSION"])
}


func TestRenderContributions_PathType_RejectsRelative(t *testing.T) {
	raw := `
name: home-override
options:
  home_directory:
    type: project-path
    required: true
contributes:
  runtime:
    extra_builds:
      - "COPY {{ .plugin.options.home_directory }} /opt/home-seed"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{"home_directory": "./my-home"}
	_, err = RenderContributions(p, opts, RenderContext{Self: map[string]any{"name": "agent-01"}})
	assert.ErrorContains(t, err, "must use @fleet/ prefix")
}

func TestRenderContributions_PathType_RejectsTraversal(t *testing.T) {
	raw := `
name: home-override
options:
  home_directory:
    type: project-path
    required: true
contributes:
  runtime:
    extra_builds:
      - "COPY {{ .plugin.options.home_directory }} /opt/home-seed"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{"home_directory": "../evil"}
	_, err = RenderContributions(p, opts, RenderContext{Self: map[string]any{"name": "agent-01"}})
	assert.ErrorContains(t, err, "must use @fleet/ prefix")
}

func TestRenderContributions_PathType_AcceptsFleet(t *testing.T) {
	raw := `
name: home-override
options:
  home_directory:
    type: project-path
    required: true
contributes:
  runtime:
    extra_builds:
      - "COPY {{ .plugin.options.home_directory }} /opt/home-seed"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{"home_directory": "@fleet/shared-home"}
	rendered, err := RenderContributions(p, opts, RenderContext{Self: map[string]any{"name": "agent-01"}})
	require.NoError(t, err)

	// @fleet/shared-home → shared-home (project-root-relative)
	assert.Equal(t, "COPY shared-home /opt/home-seed", rendered.Runtime.ExtraBuilds[0])
}

func TestRenderContributions_FleetPath_InStringType(t *testing.T) {
	// @fleet/ works in type: string options too
	raw := `
name: test-plugin
options:
  data_dir:
    type: string
    required: true
contributes:
  runtime:
    extra_builds:
      - "COPY {{ .plugin.options.data_dir }} /data"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{"data_dir": "@fleet/shared/data"}
	rendered, err := RenderContributions(p, opts, RenderContext{Self: map[string]any{"name": "agent-01"}})
	require.NoError(t, err)

	// @fleet/shared/data → shared/data (project-root-relative)
	assert.Equal(t, "COPY shared/data /data", rendered.Runtime.ExtraBuilds[0])
}

func TestRenderContributions_FleetPath_NotBlocked(t *testing.T) {
	// @fleet/ paths should NOT trigger path traversal validation
	raw := `
name: test-plugin
options:
  data_dir:
    type: string
    required: true
contributes:
  runtime:
    extra_builds:
      - "COPY {{ .plugin.options.data_dir }} /data"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{"data_dir": "@fleet/shared/data"}
	_, err = RenderContributions(p, opts, RenderContext{Self: map[string]any{"name": "agent-01"}})
	assert.NoError(t, err)
}

func TestResolveFleetPaths(t *testing.T) {
	tests := []struct {
		name     string
		opts     map[string]any
		wantOpts map[string]any
	}{
		{
			name:     "strips @fleet/ prefix",
			opts:     map[string]any{"home_directory": "@fleet/shared-home"},
			wantOpts: map[string]any{"home_directory": "shared-home"},
		},
		{
			name:     "nested fleet path",
			opts:     map[string]any{"config": "@fleet/shared/config/app.yaml"},
			wantOpts: map[string]any{"config": "shared/config/app.yaml"},
		},
		{
			name:     "non-fleet paths unchanged",
			opts:     map[string]any{"local": "./my-dir", "abs": "/data/foo"},
			wantOpts: map[string]any{"local": "./my-dir", "abs": "/data/foo"},
		},
		{
			name:     "non-string values unchanged",
			opts:     map[string]any{"port": 8080, "enabled": true},
			wantOpts: map[string]any{"port": 8080, "enabled": true},
		},
		{
			name:     "multiple options mixed",
			opts:     map[string]any{"dir": "@fleet/data", "name": "my-agent", "path": "@fleet/keys/id.pub"},
			wantOpts: map[string]any{"dir": "data", "name": "my-agent", "path": "keys/id.pub"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolveFleetPaths(tt.opts)
			assert.Equal(t, tt.wantOpts, tt.opts)
		})
	}
}

func TestRenderContributions_AllowsNewEgressFormat(t *testing.T) {
	raw := `
name: modern-plugin
contributes:
  gateway:
    egress:
      - hosts: ["api.example.com"]
        middlewares:
          - "./src/auth.ts"
`
	p, err := ParsePluginYAML([]byte(raw))
	require.NoError(t, err)

	opts := map[string]any{}
	rendered, err := RenderContributions(p, opts, RenderContext{Self: map[string]any{"name": "test-agent"}})
	require.NoError(t, err)
	assert.Len(t, rendered.Gateway.Egress, 1)
	assert.Equal(t, "api.example.com", rendered.Gateway.Egress[0].Hosts[0])
}
