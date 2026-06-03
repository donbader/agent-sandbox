package generate

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestMergedEnvVars_Sorted(t *testing.T) {
	t.Run("env vars are sorted alphabetically", func(t *testing.T) {
		dir := t.TempDir()
		g := &Generator{
			Config: &config.AgentConfig{
				Name:    "test",
				Runtime: "codex",
				Features: []config.FeatureEntry{
					{
						Name: "telegram",
						Config: map[string]any{
							"bot_token": "${TELEGRAM_BOT_TOKEN}",
							"access_control": map[string]any{
								"allowed_users": []any{"${ALLOWED_USER}"},
							},
						},
					},
					{
						Name: "github-pat",
						Config: map[string]any{
							"token": "${GITHUB_TOKEN}",
						},
					},
				},
			},
			Dir: dir,
		}

		vars := g.mergedEnvVars()
		assert.Equal(t, []string{"ALLOWED_USER", "GITHUB_TOKEN", "TELEGRAM_BOT_TOKEN"}, vars)
	})

	t.Run("empty features returns nil", func(t *testing.T) {
		dir := t.TempDir()
		g := &Generator{
			Config: &config.AgentConfig{
				Name:     "test",
				Runtime:  "codex",
				Features: nil,
			},
			Dir: dir,
		}

		vars := g.mergedEnvVars()
		assert.Empty(t, vars)
	})
}

func TestScanConfigEnvVars(t *testing.T) {
	t.Run("finds env vars across features", func(t *testing.T) {
		features := []config.FeatureEntry{
			{
				Name:   "a",
				Config: map[string]any{"key": "${ZZZ_VAR}"},
			},
			{
				Name:   "b",
				Config: map[string]any{"key": "${AAA_VAR}"},
			},
		}

		vars := ScanConfigEnvVars(features)
		// ScanConfigEnvVars returns insertion order (not sorted) — sorting is done by caller
		assert.Contains(t, vars, "ZZZ_VAR")
		assert.Contains(t, vars, "AAA_VAR")
		assert.Len(t, vars, 2)
	})

	t.Run("deduplicates across features", func(t *testing.T) {
		features := []config.FeatureEntry{
			{
				Name:   "a",
				Config: map[string]any{"key": "${SHARED_VAR}"},
			},
			{
				Name:   "b",
				Config: map[string]any{"key": "${SHARED_VAR}"},
			},
		}

		vars := ScanConfigEnvVars(features)
		assert.Equal(t, []string{"SHARED_VAR"}, vars)
	})
}
