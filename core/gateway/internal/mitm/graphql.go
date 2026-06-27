package mitm

import (
	"encoding/json"
	"regexp"
)

// graphqlMutationRe matches a named mutation: mutation MergePullRequest(...)
var graphqlMutationRe = regexp.MustCompile(`(?i)mutation\s+(\w+)`)

// graphqlAnonMutationRe matches an anonymous mutation's first field: mutation { mergePullRequest(...)
var graphqlAnonMutationRe = regexp.MustCompile(`(?is)mutation\s*\{\s*(\w+)`)

// ExtractGraphQLMutation extracts the mutation name from a GraphQL JSON request body.
// Priority: operationName field > named mutation regex > anonymous mutation field name.
// Returns "" if the body is not valid JSON or contains no mutation name.
func ExtractGraphQLMutation(body []byte) string {
	var gqlReq struct {
		Query         string `json:"query"`
		OperationName string `json:"operationName"`
	}
	if err := json.Unmarshal(body, &gqlReq); err != nil {
		return ""
	}
	if gqlReq.OperationName != "" {
		return gqlReq.OperationName
	}
	// Try named mutation: mutation MergePullRequest(...) { ... }
	if m := graphqlMutationRe.FindStringSubmatch(gqlReq.Query); len(m) > 1 {
		return m[1]
	}
	// Try anonymous mutation: mutation { mergePullRequest(...) { ... } }
	if m := graphqlAnonMutationRe.FindStringSubmatch(gqlReq.Query); len(m) > 1 {
		return m[1]
	}
	return ""
}
