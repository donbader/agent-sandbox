package mitm

import (
	"net/http"
	"testing"
)

func TestAuthHeaderRewriter_RewriteRequest(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_testtoken123")

	rw, err := NewAuthHeaderRewriter(
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
		ok := rw.RewriteRequest(req)
		if !ok {
			t.Error("expected rewrite to succeed")
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
		ok := rw.RewriteRequest(req)
		if !ok {
			t.Error("expected rewrite to succeed")
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
		ok := rw.RewriteRequest(req)
		if ok {
			t.Error("expected rewrite to not match")
		}
		if req.Header.Get("Authorization") != "" {
			t.Error("expected no Authorization header to be set")
		}
	})

	t.Run("strips port before domain match", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "https://api.github.com:443/repos", nil)
		req.Host = "api.github.com:443"
		ok := rw.RewriteRequest(req)
		if !ok {
			t.Error("expected rewrite to succeed with port in host")
		}
		if req.Header.Get("Authorization") == "" {
			t.Error("expected Authorization header to be set")
		}
	})
}

func TestAuthHeaderRewriter_ValueFormats(t *testing.T) {
	t.Setenv("MY_TOKEN", "secret123")

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
			rw, err := NewAuthHeaderRewriter([]string{"example.com"}, "Authorization", tc.valueFormat, "MY_TOKEN")
			if err != nil {
				t.Fatal(err)
			}
			req, _ := http.NewRequest("GET", "https://example.com/path", nil)
			req.Host = "example.com"
			rw.RewriteRequest(req)
			got := req.Header.Get("Authorization")
			if got != tc.wantHeader {
				t.Errorf("valueFormat %q: expected %q, got %q", tc.valueFormat, tc.wantHeader, got)
			}
		})
	}
}

func TestNewAuthHeaderRewriter_MissingEnvVar(t *testing.T) {
	// Ensure the env var is not set
	t.Setenv("MISSING_TOKEN", "")

	_, err := NewAuthHeaderRewriter([]string{"example.com"}, "Authorization", "token ${value}", "MISSING_TOKEN")
	if err == nil {
		t.Error("expected error when env var is not set")
	}
}

func TestNewAuthHeaderRewriter_DefaultValueFormat(t *testing.T) {
	t.Setenv("BARE_TOKEN", "rawvalue")

	rw, err := NewAuthHeaderRewriter([]string{"example.com"}, "X-API-Key", "", "BARE_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", "https://example.com/path", nil)
	req.Host = "example.com"
	rw.RewriteRequest(req)
	got := req.Header.Get("X-API-Key")
	if got != "rawvalue" {
		t.Errorf("expected bare value %q, got %q", "rawvalue", got)
	}
}
