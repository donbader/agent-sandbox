package custom

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"time"
)

// PendingOAuthFlow stores state for an in-progress OAuth login.
type PendingOAuthFlow struct {
	Provider     string
	CodeVerifier string
	RedirectURI  string
	CreatedAt    time.Time
}

// pendingFlows maps state parameter → pending flow.
// Shared between login handler (writes) and callback handler (reads + deletes).
var pendingFlows = struct {
	sync.Mutex
	m map[string]*PendingOAuthFlow
}{m: make(map[string]*PendingOAuthFlow)}

// StorePendingFlow stores a new pending OAuth flow keyed by state.
func StorePendingFlow(state string, flow *PendingOAuthFlow) {
	pendingFlows.Lock()
	defer pendingFlows.Unlock()
	pendingFlows.m[state] = flow
}

// ConsumePendingFlow retrieves and removes a pending flow by state.
// Returns nil if not found or expired (>10 minutes).
func ConsumePendingFlow(state string) *PendingOAuthFlow {
	pendingFlows.Lock()
	defer pendingFlows.Unlock()
	flow, ok := pendingFlows.m[state]
	if !ok {
		return nil
	}
	delete(pendingFlows.m, state)
	if time.Since(flow.CreatedAt) > 10*time.Minute {
		return nil
	}
	return flow
}

// GenerateCodeVerifier generates a cryptographically random PKCE code_verifier (43-128 chars).
func GenerateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CodeChallengeS256 derives the S256 code_challenge from a code_verifier.
func CodeChallengeS256(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// GenerateState generates a cryptographically random state parameter.
func GenerateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
