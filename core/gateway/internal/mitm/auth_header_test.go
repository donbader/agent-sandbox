package mitm

import (
	"net/http"
	"testing"

	"github.com/donbader/agent-sandbox/core/sdk/gateway"
)

func TestRegisterAuthHeaderMiddleware_InjectsHeader(t *testing.T) {
	gateway.ResetForTesting()
	t.Setenv("GITHUB_TOKEN", "ghp_testtoken123")

	err := RegisterAuthHeaderMiddleware(
		"test-github",
		[]string{"api.github.com", "github.com"},
		"Authorization",
		"token ${value}",
		"GITHUB_TOKEN",
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("injects header for matching domain", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "https://api.github.com/repos/owner/repo", nil)
		req.Host = "api.github.com"

		matching := gateway.MatchingMiddleware(req)
		if len(matching) == 0 {
			t.Fatal("expected middleware to match")
		}
		ctx := &gateway.MiddlewareContext{Request: req, Env: func(k string) string { return "" }}
		if err := matching[0].Func(ctx); err != nil {
			t.Fatal(err)
		}

		got := req.Header.Get("Authorization")
		want := "token ghp_testtoken123"
		if got != want {
			t.Errorf("expected header %q, got %q", want, got)
		}
	})

	t.Run("injects header for second matching domain", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "https://github.com/owner/repo", nil)
		req.Host = "github.com"

		matching := gateway.MatchingMiddleware(req)
		if len(matching) == 0 {
			t.Fatal("expected middleware to match")
		}
		ctx := &gateway.MiddlewareContext{Request: req, Env: func(k string) string { return "" }}
		if err := matching[0].Func(ctx); err != nil {
			t.Fatal(err)
		}

		got := req.Header.Get("Authorization")
		want := "token ghp_testtoken123"
		if got != want {
			t.Errorf("expected header %q, got %q", want, got)
		}
	})

	t.Run("skips non-matching domain", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "https://other.example.com/path", nil)
		req.Host = "other.example.com"

		matching := gateway.MatchingMiddleware(req)
		if len(matching) != 0 {
			t.Error("expected no middleware to match")
		}
	})

	t.Run("strips port before domain match", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "https://api.github.com:443/repos", nil)
		req.Host = "api.github.com:443"

		matching := gateway.MatchingMiddleware(req)
		if len(matching) == 0 {
			t.Fatal("expected middleware to match with port in host")
		}
	})
}

func TestRegisterAuthHeaderMiddleware_ValueFormats(t *testing.T) {
	tests := []struct {
		name        string
		valueFormat string
		wantHeader  string
	}{
		{"bare value", "${value}", "secret123"},
		{"bearer prefix", "Bearer ${value}", "Bearer secret123"},
		{"token prefix", "token ${value}", "token secret123"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gateway.ResetForTesting()
			t.Setenv("MY_TOKEN", "secret123")

			err := RegisterAuthHeaderMiddleware("test", []string{"example.com"}, "Authorization", tc.valueFormat, "MY_TOKEN")
			if err != nil {
				t.Fatal(err)
			}

			req, _ := http.NewRequest("GET", "https://example.com/path", nil)
			req.Host = "example.com"

			matching := gateway.MatchingMiddleware(req)
			if len(matching) == 0 {
				t.Fatal("expected middleware to match")
			}
			ctx := &gateway.MiddlewareContext{Request: req, Env: func(k string) string { return "" }}
			if err := matching[0].Func(ctx); err != nil {
				t.Fatal(err)
			}

			got := req.Header.Get("Authorization")
			if got != tc.wantHeader {
				t.Errorf("valueFormat %q: expected %q, got %q", tc.valueFormat, tc.wantHeader, got)
			}
		})
	}
}

func TestRegisterAuthHeaderMiddleware_MissingEnvVar(t *testing.T) {
	gateway.ResetForTesting()
	t.Setenv("MISSING_TOKEN", "")

	err := RegisterAuthHeaderMiddleware("test", []string{"example.com"}, "Authorization", "token ${value}", "MISSING_TOKEN")
	if err == nil {
		t.Error("expected error when env var is not set")
	}
}

func TestRegisterAuthHeaderMiddleware_DefaultValueFormat(t *testing.T) {
	gateway.ResetForTesting()
	t.Setenv("BARE_TOKEN", "rawvalue")

	err := RegisterAuthHeaderMiddleware("test", []string{"example.com"}, "X-API-Key", "", "BARE_TOKEN")
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("GET", "https://example.com/path", nil)
	req.Host = "example.com"

	matching := gateway.MatchingMiddleware(req)
	if len(matching) == 0 {
		t.Fatal("expected middleware to match")
	}
	ctx := &gateway.MiddlewareContext{Request: req, Env: func(k string) string { return "" }}
	if err := matching[0].Func(ctx); err != nil {
		t.Fatal(err)
	}

	got := req.Header.Get("X-API-Key")
	if got != "rawvalue" {
		t.Errorf("expected bare value %q, got %q", "rawvalue", got)
	}
}
