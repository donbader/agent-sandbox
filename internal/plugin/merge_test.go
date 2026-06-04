package plugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeContributions(t *testing.T) {
	a := &Contributions{
		Runtime: RuntimeContrib{ExtraBuilds: []string{"RUN apt-get install -y git"}},
		Gateway: GatewayContrib{Services: []GatewayService{
			{URL: "https://github.com", Headers: map[string]string{"Authorization": "Bearer abc"}},
		}},
	}
	b := &Contributions{
		Runtime: RuntimeContrib{ExtraBuilds: []string{"RUN npm install -g codex-acp"}},
		Gateway: GatewayContrib{Services: []GatewayService{
			{URL: "https://api.telegram.org"},
		}},
		Sidecar: SidecarContrib{Services: map[string]ComposeService{
			"telegram": {Build: "./sidecar"},
		}},
	}

	merged := MergeContributions(a, b)

	assert.Len(t, merged.Runtime.ExtraBuilds, 2)
	assert.Len(t, merged.Gateway.Services, 2)
	assert.Len(t, merged.Sidecar.Services, 1)
	assert.Contains(t, merged.Sidecar.Services, "telegram")
}

func TestMergeContributions_NilHandling(t *testing.T) {
	a := &Contributions{
		Runtime: RuntimeContrib{ExtraBuilds: []string{"RUN echo hello"}},
	}

	merged := MergeContributions(nil, a, nil)

	assert.Len(t, merged.Runtime.ExtraBuilds, 1)
	assert.Equal(t, "RUN echo hello", merged.Runtime.ExtraBuilds[0])
}

func TestMergeContributions_Empty(t *testing.T) {
	merged := MergeContributions()
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

	merged := MergeContributions(a, b)

	assert.Equal(t, []string{"/usr/sbin/sshd -p 2222", "/usr/bin/some-daemon"}, merged.Runtime.PreEntrypoint)
	assert.Equal(t, []string{"2222:2222", "8080:8080"}, merged.Runtime.Ports)
}
