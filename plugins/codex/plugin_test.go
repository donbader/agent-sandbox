package codex

import (
	"testing"

	"github.com/donbader/agent-sandbox/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlugin_Name(t *testing.T) {
	p := New()
	assert.Equal(t, "codex", p.Name())
}

func TestPlugin_Contribute(t *testing.T) {
	p := New()
	contrib, err := p.Contribute(sdk.ContributeContext{
		AgentName: "test",
		Config:    nil,
	})
	require.NoError(t, err)
	assert.Equal(t, "node:22-slim", contrib.BaseImage)
	assert.Contains(t, contrib.Commands[0], "npm install -g @openai/codex")
	assert.Equal(t, []string{"codex", "--full-auto"}, contrib.Cmd)
}
