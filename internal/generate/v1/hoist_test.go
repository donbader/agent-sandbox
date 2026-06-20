package v1

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/stretchr/testify/assert"
)

func TestCollectArtifactPaths(t *testing.T) {
	stages := []plugin.NamedBuildStage{
		{
			Name: "telegram",
			Artifacts: []config.StageArtifact{
				{From: "/src/dist", To: "/opt/telegram-adapter/dist"},
				{From: "/src/node_modules", To: "/opt/telegram-adapter/node_modules"},
			},
		},
		{
			Name: "pi-acp",
			Artifacts: []config.StageArtifact{
				{From: "/src/pi-acp.tgz", To: "/tmp/pi-acp.tgz"},
			},
		},
	}

	paths := collectArtifactPaths(stages)

	// Should include direct paths and parent dirs (where deep enough)
	assert.Contains(t, paths, "/opt/telegram-adapter/dist")
	assert.Contains(t, paths, "/opt/telegram-adapter/node_modules")
	assert.Contains(t, paths, "/opt/telegram-adapter/") // parent dir
	assert.Contains(t, paths, "/tmp/pi-acp.tgz")
	// /tmp/ is too shallow (depth <= 2), should NOT be included
	assert.NotContains(t, paths, "/tmp/")
}

func TestParentDir(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/opt/telegram-adapter/dist", "/opt/telegram-adapter/"},
		{"/opt/agent-manager/node_modules", "/opt/agent-manager/"},
		{"/tmp/pi-acp.tgz", ""},   // too shallow
		{"/foo", ""},               // too shallow
		{"relative/path", ""},      // no leading slash
		{"/a/b/c/d", "/a/b/c/"},   // deep path
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parentDir(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsLateBuild(t *testing.T) {
	artifactPaths := []string{
		"/opt/telegram-adapter/dist",
		"/opt/telegram-adapter/",
		"/tmp/pi-acp.tgz",
	}

	tests := []struct {
		step string
		late bool
	}{
		// COPY is always late
		{"COPY dorey-home /opt/home-seed/", true},
		{"COPY .build/foo/bar.sh /opt/bar.sh", true},
		// References artifact path
		{"RUN npm install -g /tmp/pi-acp.tgz && rm /tmp/pi-acp.tgz", true},
		{"RUN echo 'hi' > /opt/telegram-adapter/config.json", true},
		// Heavy installs — no artifact reference, should be early
		{"RUN curl -fsSL https://get.docker.com | sh", false},
		{"RUN apt-get update && apt-get install -y python3", false},
		{"RUN npm install -g agent-browser@0.27.3", false},
		{"ENV GH_TOKEN=dummy GITHUB_TOKEN=dummy", false},
	}
	for _, tt := range tests {
		t.Run(tt.step[:min(len(tt.step), 40)], func(t *testing.T) {
			got := isLateBuild(tt.step, artifactPaths)
			assert.Equal(t, tt.late, got)
		})
	}
}

func TestExtractCopyDest(t *testing.T) {
	tests := []struct {
		step string
		want string
	}{
		{"COPY dorey-home /opt/home-seed/", "/opt/home-seed"},
		{"COPY .build/foo/bar.sh /opt/headroom/start.sh", "/opt/headroom"},
		{"COPY src/ /app/", "/app"},
		{"RUN echo hello", ""},  // not a COPY
		{"COPY single", ""},     // not enough args
	}
	for _, tt := range tests {
		t.Run(tt.step, func(t *testing.T) {
			got := extractCopyDest(tt.step)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSplitExtraBuilds(t *testing.T) {
	artifactPaths := []string{
		"/opt/agent-manager/dist",
		"/opt/agent-manager/",
		"/opt/telegram-adapter/dist",
		"/opt/telegram-adapter/",
		"/tmp/pi-acp.tgz",
	}

	steps := []string{
		// Early: heavy installs
		"RUN apt-get update && apt-get install -y gh",
		"RUN curl -fsSL https://get.docker.com | sh",
		"RUN npm install -g agent-browser@0.27.3",
		"ENV GH_TOKEN=dummy",
		// Late: COPY from build context
		"COPY dorey-home /opt/home-seed/",
		// Late: references artifact path
		"RUN npm install -g /tmp/pi-acp.tgz && rm /tmp/pi-acp.tgz",
		"RUN echo '{}' > /opt/agent-manager/config.json",
		// Late: references COPY dest via pass 2
		"RUN chown -R agent:agent /opt/home-seed",
	}

	early, late := splitExtraBuilds(steps, artifactPaths)

	assert.Equal(t, []string{
		"RUN apt-get update && apt-get install -y gh",
		"RUN curl -fsSL https://get.docker.com | sh",
		"RUN npm install -g agent-browser@0.27.3",
		"ENV GH_TOKEN=dummy",
	}, early)

	assert.Equal(t, []string{
		"COPY dorey-home /opt/home-seed/",
		"RUN npm install -g /tmp/pi-acp.tgz && rm /tmp/pi-acp.tgz",
		"RUN echo '{}' > /opt/agent-manager/config.json",
		"RUN chown -R agent:agent /opt/home-seed",
	}, late)
}

func TestSplitExtraBuilds_NoStages(t *testing.T) {
	// When no build stages, all steps are early (except COPY)
	steps := []string{
		"RUN apt-get install -y curl",
		"COPY config.json /etc/app/",
		"RUN echo done",
	}

	early, late := splitExtraBuilds(steps, nil)

	assert.Equal(t, []string{"RUN apt-get install -y curl", "RUN echo done"}, early)
	assert.Equal(t, []string{"COPY config.json /etc/app/"}, late)
}
