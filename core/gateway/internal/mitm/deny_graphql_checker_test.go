package mitm

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// makeDenyGraphQLChecker mirrors the DenyGraphQLChecker closure wired in
// core/gateway/cmd/gateway/main.go. Extracted here so we can test the logic
// without spinning up the full gateway binary.
func makeDenyGraphQLChecker(deniedMutations []string) func(host string, req *http.Request) bool {
	return func(host string, req *http.Request) bool {
		if req.Method != http.MethodPost {
			return false
		}
		if !strings.Contains(strings.ToLower(req.URL.Path), "graphql") {
			return false
		}
		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			return false
		}
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		mutationName := ExtractGraphQLMutation(bodyBytes)
		if mutationName == "" {
			return false
		}
		for _, denied := range deniedMutations {
			if strings.EqualFold(denied, mutationName) {
				return true
			}
		}
		return false
	}
}

// mustNewRequest is a test helper that creates an http.Request or fails the test.
func mustNewRequest(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestDenyGraphQLChecker_BlocksDeniedMutation(t *testing.T) {
	checker := makeDenyGraphQLChecker([]string{"mergePullRequest"})

	body := `{"operationName":"mergePullRequest","query":"mutation mergePullRequest($id: ID!) { mergePullRequest(input: {pullRequestId: $id}) { pullRequest { merged } } }"}`
	req := mustNewRequest(t, http.MethodPost, "https://api.github.com/graphql", body)

	assert.True(t, checker("api.github.com", req), "denied mutation should be blocked with 403")
}

func TestDenyGraphQLChecker_AllowsQuery(t *testing.T) {
	checker := makeDenyGraphQLChecker([]string{"mergePullRequest"})

	body := `{"query":"query GetPR($id: ID!) { pullRequest(id: $id) { title } }"}`
	req := mustNewRequest(t, http.MethodPost, "https://api.github.com/graphql", body)

	assert.False(t, checker("api.github.com", req), "GraphQL query (not mutation) should pass through")
}

func TestDenyGraphQLChecker_AllowsNonGraphQLPath(t *testing.T) {
	checker := makeDenyGraphQLChecker([]string{"mergePullRequest"})

	// Same mutation body but posted to a non-graphql path
	body := `{"operationName":"mergePullRequest","query":"mutation mergePullRequest { }"}`
	req := mustNewRequest(t, http.MethodPost, "https://api.github.com/api/v3/repos/owner/repo/pulls/1/merge", body)

	assert.False(t, checker("api.github.com", req), "mutation body on non-graphql path should not be blocked")
}

func TestDenyGraphQLChecker_AllowsGET(t *testing.T) {
	checker := makeDenyGraphQLChecker([]string{"mergePullRequest"})

	req := mustNewRequest(t, http.MethodGet, "https://api.github.com/graphql", "")

	assert.False(t, checker("api.github.com", req), "GET /graphql should not be blocked (only POST is GraphQL)")
}

func TestDenyGraphQLChecker_AllowsUndeniedMutation(t *testing.T) {
	checker := makeDenyGraphQLChecker([]string{"mergePullRequest"})

	body := `{"operationName":"createIssue","query":"mutation createIssue { createIssue { id } }"}`
	req := mustNewRequest(t, http.MethodPost, "https://api.github.com/graphql", body)

	assert.False(t, checker("api.github.com", req), "mutation not in deny list should pass through")
}

func TestDenyGraphQLChecker_CaseInsensitiveMatch(t *testing.T) {
	// deny list uses mixed case; mutation name in body uses different case
	checker := makeDenyGraphQLChecker([]string{"MergePullRequest"})

	body := `{"operationName":"mergePullRequest","query":"mutation mergePullRequest { }"}`
	req := mustNewRequest(t, http.MethodPost, "https://api.github.com/graphql", body)

	assert.True(t, checker("api.github.com", req), "mutation matching should be case-insensitive")
}

// TestHandler_DenyGraphQLChecker_Wiring verifies that Handler.DenyGraphQLChecker
// is a settable field and that the checker is invoked correctly when set.
func TestHandler_DenyGraphQLChecker_Wiring(t *testing.T) {
	ca := testCA(t)
	h := NewHandler([]string{"api.github.com"}, ca)

	// Field should be nil by default
	assert.Nil(t, h.DenyGraphQLChecker)

	// Wire the checker — same pattern as main.go
	h.DenyGraphQLChecker = makeDenyGraphQLChecker([]string{"mergePullRequest"})
	assert.NotNil(t, h.DenyGraphQLChecker)

	// Verify blocked: denied mutation on graphql path
	blocked := mustNewRequest(t, http.MethodPost, "https://api.github.com/graphql",
		`{"operationName":"mergePullRequest","query":"mutation mergePullRequest { }"}`)
	assert.True(t, h.DenyGraphQLChecker("api.github.com", blocked))

	// Verify allowed: query on graphql path
	allowed := mustNewRequest(t, http.MethodPost, "https://api.github.com/graphql",
		`{"query":"query GetViewer { viewer { login } }"}`)
	assert.False(t, h.DenyGraphQLChecker("api.github.com", allowed))
}
