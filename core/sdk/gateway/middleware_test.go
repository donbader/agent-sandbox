package gateway

import "testing"

func TestRegisterSecret(t *testing.T) {
	// Reset state
	secrets = nil

	RegisterSecret("my-secret-token")
	RegisterSecret("") // empty should be ignored
	RegisterSecret("another-secret")

	got := Secrets()
	if len(got) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(got))
	}
	if got[0] != "my-secret-token" {
		t.Errorf("expected 'my-secret-token', got %q", got[0])
	}
	if got[1] != "another-secret" {
		t.Errorf("expected 'another-secret', got %q", got[1])
	}
}
