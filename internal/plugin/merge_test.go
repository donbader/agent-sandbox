package plugin

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeContributions(t *testing.T) {
	a := &Contributions{
		Runtime: RuntimeContrib{ExtraBuilds: []string{"RUN apt-get install -y git"}},
		Gateway: GatewayContrib{Egress: []config.EgressRule{
			{Hosts: []string{"github.com"}, Headers: map[string]string{"Authorization": "Bearer abc"}},
		}},
	}
	b := &Contributions{
		Runtime: RuntimeContrib{ExtraBuilds: []string{"RUN npm install -g codex-acp"}},
		Gateway: GatewayContrib{Egress: []config.EgressRule{
			{Hosts: []string{"api.telegram.org"}},
		}},
		Sidecar: SidecarContrib{Services: map[string]ComposeService{
			"telegram": {Build: "./sidecar"},
		}},
	}

	merged, err := MergeContributions(a, b)
	require.NoError(t, err)

	assert.Len(t, merged.Runtime.ExtraBuilds, 2)
	assert.Len(t, merged.Gateway.Egress, 2)
	assert.Len(t, merged.Sidecar.Services, 1)
	assert.Contains(t, merged.Sidecar.Services, "telegram")
}

func TestMergeContributions_NilHandling(t *testing.T) {
	a := &Contributions{
		Runtime: RuntimeContrib{ExtraBuilds: []string{"RUN echo hello"}},
	}

	merged, err := MergeContributions(nil, a, nil)
	require.NoError(t, err)

	assert.Len(t, merged.Runtime.ExtraBuilds, 1)
	assert.Equal(t, "RUN echo hello", merged.Runtime.ExtraBuilds[0])
}

func TestMergeContributions_Empty(t *testing.T) {
	merged, err := MergeContributions()
	require.NoError(t, err)
	assert.NotNil(t, merged)
	assert.NotNil(t, merged.Sidecar.Services)
	assert.Empty(t, merged.Runtime.ExtraBuilds)
}

func TestMergeContributions_PreEntrypointAndPorts(t *testing.T) {
	a := &Contributions{
		Runtime: RuntimeContrib{
			PreEntrypoint: []string{"/usr/sbin/sshd -p 2222"},
			Ports:         []string{"2222:2222"},
		},
	}
	b := &Contributions{
		Runtime: RuntimeContrib{
			PreEntrypoint: []string{"/usr/bin/some-daemon"},
			Ports:         []string{"8080:8080"},
		},
	}

	merged, err := MergeContributions(a, b)
	require.NoError(t, err)

	assert.Equal(t, []string{"/usr/sbin/sshd -p 2222", "/usr/bin/some-daemon"}, merged.Runtime.PreEntrypoint)
	assert.Equal(t, []string{"2222:2222", "8080:8080"}, merged.Runtime.Ports)
}

func TestMergeContributions_CapAddDedup(t *testing.T) {
	a := &Contributions{
		Runtime: RuntimeContrib{
			CapAdd: []string{"SYS_CHROOT", "NET_ADMIN"},
		},
	}
	b := &Contributions{
		Runtime: RuntimeContrib{
			CapAdd: []string{"NET_ADMIN", "SYS_PTRACE"},
		},
	}

	merged, err := MergeContributions(a, b)
	require.NoError(t, err)

	// NET_ADMIN appears in both but should be deduplicated
	assert.Equal(t, []string{"SYS_CHROOT", "NET_ADMIN", "SYS_PTRACE"}, merged.Runtime.CapAdd)
}

func TestMergeContributions_SkipUserns(t *testing.T) {
	a := &Contributions{
		Runtime: RuntimeContrib{
			SkipUserns: false,
		},
	}
	b := &Contributions{
		Runtime: RuntimeContrib{
			SkipUserns: true,
		},
	}

	merged, err := MergeContributions(a, b)
	require.NoError(t, err)

	// Logical OR — if any plugin sets it true, result is true
	assert.True(t, merged.Runtime.SkipUserns)
}

func TestMergeContributions_SkipUserns_AllFalse(t *testing.T) {
	a := &Contributions{
		Runtime: RuntimeContrib{
			SkipUserns: false,
		},
	}
	b := &Contributions{
		Runtime: RuntimeContrib{
			SkipUserns: false,
		},
	}

	merged, err := MergeContributions(a, b)
	require.NoError(t, err)

	assert.False(t, merged.Runtime.SkipUserns)
}

func TestMergeContributions_BuildStages(t *testing.T) {
	a := &Contributions{
		Runtime: RuntimeContrib{
			BuildStages: []NamedBuildStage{
				{Name: "plugin-a", Base: "golang:1.24", Steps: []string{"RUN go build -o /app ./cmd"}},
			},
		},
	}
	b := &Contributions{
		Runtime: RuntimeContrib{
			BuildStages: []NamedBuildStage{
				{Name: "plugin-b", Steps: []string{"RUN npm ci"}},
			},
		},
	}

	merged, err := MergeContributions(a, b)
	require.NoError(t, err)

	require.Len(t, merged.Runtime.BuildStages, 2)
	assert.Equal(t, "plugin-a", merged.Runtime.BuildStages[0].Name)
	assert.Equal(t, "golang:1.24", merged.Runtime.BuildStages[0].Base)
	assert.Equal(t, "plugin-b", merged.Runtime.BuildStages[1].Name)
	// Order must be preserved
	assert.Equal(t, []string{"RUN npm ci"}, merged.Runtime.BuildStages[1].Steps)
}

func TestMergeContributions_Ingress(t *testing.T) {
	a := &Contributions{
		Gateway: GatewayContrib{
			Ingress: []IngressRule{
				{Listen: "2222", Target: "agent-a:2222"},
			},
		},
	}
	b := &Contributions{
		Gateway: GatewayContrib{
			Ingress: []IngressRule{
				{Listen: "8080", Target: "agent-b:8080"},
			},
		},
	}

	merged, err := MergeContributions(a, b)
	require.NoError(t, err)

	require.Len(t, merged.Gateway.Ingress, 2)
	assert.Equal(t, "2222", merged.Gateway.Ingress[0].Listen)
	assert.Equal(t, "agent-a:2222", merged.Gateway.Ingress[0].Target)
	assert.Equal(t, "8080", merged.Gateway.Ingress[1].Listen)
	assert.Equal(t, "agent-b:8080", merged.Gateway.Ingress[1].Target)
}

func TestMergeContributions_SidecarCollision(t *testing.T) {
	a := &Contributions{
		Sidecar: SidecarContrib{Services: map[string]ComposeService{
			"db": {Image: "postgres:16"},
		}},
	}
	b := &Contributions{
		Sidecar: SidecarContrib{Services: map[string]ComposeService{
			"db": {Image: "mysql:8"},
		}},
	}

	_, err := MergeContributions(a, b)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"db"`)
}
