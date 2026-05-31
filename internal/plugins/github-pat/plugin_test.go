package githubpat

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/resolve"
)

func TestGitHubPATPlugin_DefaultDomains(t *testing.T) {
	plugin := resolve.RegisteredPlugins()["github-pat"]
	if plugin == nil {
		t.Fatal("github-pat plugin not registered")
	}

	contrib, err := plugin.Resolve("", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	wantDomains := []string{"api.github.com", "github.com"}
	if len(contrib.MITMDomains) != len(wantDomains) {
		t.Fatalf("expected MITMDomains %v, got %v", wantDomains, contrib.MITMDomains)
	}
	for i, d := range wantDomains {
		if contrib.MITMDomains[i] != d {
			t.Errorf("MITMDomains[%d]: expected %q, got %q", i, d, contrib.MITMDomains[i])
		}
	}

	if len(contrib.EnvVars) != 1 || contrib.EnvVars[0] != "GITHUB_TOKEN" {
		t.Errorf("expected EnvVars=[GITHUB_TOKEN], got %v", contrib.EnvVars)
	}

	wantAgentEnv := []string{"GH_TOKEN=dummy", "GITHUB_TOKEN=dummy"}
	if len(contrib.AgentEnv) != len(wantAgentEnv) {
		t.Fatalf("expected AgentEnv %v, got %v", wantAgentEnv, contrib.AgentEnv)
	}
	for i, e := range wantAgentEnv {
		if contrib.AgentEnv[i] != e {
			t.Errorf("AgentEnv[%d]: expected %q, got %q", i, e, contrib.AgentEnv[i])
		}
	}

	if len(contrib.Rewriters) != 1 {
		t.Fatalf("expected 1 rewriter, got %d", len(contrib.Rewriters))
	}
	rw := contrib.Rewriters[0]
	if rw.Type != "auth-header" {
		t.Errorf("expected rewriter type %q, got %q", "auth-header", rw.Type)
	}
	if rw.Header != "Authorization" {
		t.Errorf("expected header %q, got %q", "Authorization", rw.Header)
	}
	if rw.ValueFormat != "Basic ${base64_basic}" {
		t.Errorf("expected value_format %q, got %q", "Basic ${base64_basic}", rw.ValueFormat)
	}
	if rw.EnvVar != "GITHUB_TOKEN" {
		t.Errorf("expected env_var %q, got %q", "GITHUB_TOKEN", rw.EnvVar)
	}
}

func TestGitHubPATPlugin_CustomDomains(t *testing.T) {
	plugin := resolve.RegisteredPlugins()["github-pat"]
	if plugin == nil {
		t.Fatal("github-pat plugin not registered")
	}

	contrib, err := plugin.Resolve("", map[string]any{
		"domains": []any{"api.github.com"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(contrib.MITMDomains) != 1 || contrib.MITMDomains[0] != "api.github.com" {
		t.Errorf("expected MITMDomains=[api.github.com], got %v", contrib.MITMDomains)
	}
	if len(contrib.Rewriters) != 1 || len(contrib.Rewriters[0].Domains) != 1 || contrib.Rewriters[0].Domains[0] != "api.github.com" {
		t.Errorf("expected rewriter domains=[api.github.com], got %v", contrib.Rewriters)
	}
}
