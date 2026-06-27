package mitm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractGraphQLMutation_OperationName(t *testing.T) {
	body := []byte(`{"operationName":"mergePullRequest","query":"mutation mergePullRequest($id: ID!) { mergePullRequest(input: {pullRequestId: $id}) { pullRequest { merged } } }"}`)
	assert.Equal(t, "mergePullRequest", ExtractGraphQLMutation(body))
}

func TestExtractGraphQLMutation_OperationNameTakesPrecedence(t *testing.T) {
	// operationName wins over the mutation keyword in the query string
	body := []byte(`{"operationName":"FooMutation","query":"mutation BarMutation { bar { id } }"}`)
	assert.Equal(t, "FooMutation", ExtractGraphQLMutation(body))
}

func TestExtractGraphQLMutation_QueryField(t *testing.T) {
	// No operationName — extract from query via regex
	body := []byte(`{"query":"mutation MergePullRequest($id: ID!) { mergePullRequest(input: {pullRequestId: $id}) { pullRequest { merged } } }"}`)
	assert.Equal(t, "MergePullRequest", ExtractGraphQLMutation(body))
}

func TestExtractGraphQLMutation_QueryCaseInsensitiveKeyword(t *testing.T) {
	// The (?i) flag makes the "mutation" keyword case-insensitive
	body := []byte(`{"query":"MUTATION UpperCaseMut { upperCaseMut { id } }"}`)
	assert.Equal(t, "UpperCaseMut", ExtractGraphQLMutation(body))
}

func TestExtractGraphQLMutation_NotAMutation_ReturnsEmpty(t *testing.T) {
	// A GraphQL query (not mutation) should return ""
	body := []byte(`{"query":"query GetPR($id: ID!) { pullRequest(id: $id) { title } }"}`)
	assert.Equal(t, "", ExtractGraphQLMutation(body))
}

func TestExtractGraphQLMutation_NoQueryOrOperationName(t *testing.T) {
	body := []byte(`{"variables":{"id":"abc"}}`)
	assert.Equal(t, "", ExtractGraphQLMutation(body))
}

func TestExtractGraphQLMutation_MalformedJSON(t *testing.T) {
	assert.Equal(t, "", ExtractGraphQLMutation([]byte(`not json at all`)))
}

func TestExtractGraphQLMutation_EmptyBody(t *testing.T) {
	assert.Equal(t, "", ExtractGraphQLMutation([]byte{}))
}

func TestExtractGraphQLMutation_EmptyOperationNameFallsBackToQuery(t *testing.T) {
	// operationName present but empty — falls back to query regex
	body := []byte(`{"operationName":"","query":"mutation CreateIssue { createIssue { id } }"}`)
	assert.Equal(t, "CreateIssue", ExtractGraphQLMutation(body))
}
