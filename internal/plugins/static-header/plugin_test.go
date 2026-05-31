package staticheader

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/resolve"
)

func TestStaticHeaderPlugin_Resolve(t *testing.T) {
	plugin := resolve.RegisteredPlugins()["static-header"]
	if plugin == nil {
		t.Fatal("static-header plugin not registered")
	}

	contrib, err := plugin.Resolve("", map[string]any{
		"domains":      []any{"api.example.com"},
		"header":       "X-API-Key",
		"value_format": "Bearer ${value}",
		"env_var":      "EXAMPLE_API_KEY",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(contrib.MITMDomains) != 1 || contrib.MITMDomains[0] != "api.example.com" {
		t.Errorf("expected MITMDomains=[api.example.com], got %v", contrib.MITMDomains)
	}
	if len(contrib.EnvVars) != 1 || contrib.EnvVars[0] != "EXAMPLE_API_KEY" {
		t.Errorf("expected EnvVars=[EXAMPLE_API_KEY], got %v", contrib.EnvVars)
	}

	if len(contrib.Rewriters) != 1 {
		t.Fatalf("expected 1 rewriter, got %d", len(contrib.Rewriters))
	}
	rw := contrib.Rewriters[0]
	if rw.Type != "auth-header" {
		t.Errorf("expected type %q, got %q", "auth-header", rw.Type)
	}
	if rw.Header != "X-API-Key" {
		t.Errorf("expected header %q, got %q", "X-API-Key", rw.Header)
	}
	if rw.ValueFormat != "Bearer ${value}" {
		t.Errorf("expected value_format %q, got %q", "Bearer ${value}", rw.ValueFormat)
	}
	if rw.EnvVar != "EXAMPLE_API_KEY" {
		t.Errorf("expected env_var %q, got %q", "EXAMPLE_API_KEY", rw.EnvVar)
	}
}

func TestStaticHeaderPlugin_DefaultValueFormat(t *testing.T) {
	plugin := resolve.RegisteredPlugins()["static-header"]
	if plugin == nil {
		t.Fatal("static-header plugin not registered")
	}

	contrib, err := plugin.Resolve("", map[string]any{
		"domains": []any{"api.example.com"},
		"header":  "X-API-Key",
		"env_var": "EXAMPLE_API_KEY",
		// value_format omitted — should default to "${value}"
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(contrib.Rewriters) != 1 {
		t.Fatalf("expected 1 rewriter, got %d", len(contrib.Rewriters))
	}
	if contrib.Rewriters[0].ValueFormat != "${value}" {
		t.Errorf("expected default value_format %q, got %q", "${value}", contrib.Rewriters[0].ValueFormat)
	}
}
