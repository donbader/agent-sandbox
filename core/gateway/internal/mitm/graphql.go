package mitm

import (
	"encoding/json"
	"regexp"
)

// graphqlMutationRe matches a named mutation: mutation MergePullRequest(...)
var graphqlMutationRe = regexp.MustCompile(`(?i)mutation\s+(\w+)`)

// graphqlFieldRe matches the first field inside a mutation body: { mergePullRequest(...)
// It skips past the operation name and any variable declarations to find the opening brace,
// then captures the first identifier after it.
var graphqlFieldRe = regexp.MustCompile(`(?is)mutation\b[^{]*\{\s*(\w+)`)

// ExtractGraphQLMutation extracts the mutation name from a GraphQL JSON request body.
// Priority: operationName field > named mutation regex > field name inside braces.
// Returns "" if the body is not valid JSON or contains no mutation name.
// Deprecated: use ExtractGraphQLMutationNames for deny-list checking.
func ExtractGraphQLMutation(body []byte) string {
	names := ExtractGraphQLMutationNames(body)
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

// ExtractGraphQLMutationNames extracts all candidate mutation names from a GraphQL
// JSON request body. This includes the operationName field, the named operation
// (e.g. "mutation PullRequestMerge(...)"), and the first field name inside the
// mutation body (e.g. "mergePullRequest"). Returns nil if the body is not a mutation.
func ExtractGraphQLMutationNames(body []byte) []string {
	var gqlReq struct {
		Query         string `json:"query"`
		OperationName string `json:"operationName"`
	}
	if err := json.Unmarshal(body, &gqlReq); err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var names []string
	add := func(name string) {
		lower := toLower(name)
		if name != "" && !seen[lower] {
			seen[lower] = true
			names = append(names, name)
		}
	}

	// 1. operationName from JSON field
	add(gqlReq.OperationName)

	// 2. Named operation from query: mutation PullRequestMerge(...)
	if m := graphqlMutationRe.FindStringSubmatch(gqlReq.Query); len(m) > 1 {
		add(m[1])
	}

	// 3. First field inside mutation body: { mergePullRequest(...) }
	if m := graphqlFieldRe.FindStringSubmatch(gqlReq.Query); len(m) > 1 {
		add(m[1])
	}

	return names
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
