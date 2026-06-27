package mitm

import (
	"encoding/json"
	"regexp"
)

// graphqlMutationRe matches the mutation name in a GraphQL query string.
var graphqlMutationRe = regexp.MustCompile(`(?i)mutation\s+(\w+)`)

// ExtractGraphQLMutation extracts the mutation name from a GraphQL JSON request body.
// It first checks the operationName field, then falls back to parsing the query field
// via regex. Returns "" if the body is not valid JSON or contains no mutation name.
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
	if m := graphqlMutationRe.FindStringSubmatch(gqlReq.Query); len(m) > 1 {
		return m[1]
	}
	return ""
}
