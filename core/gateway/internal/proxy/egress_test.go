package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEgressFilter_NoRules_AllowAll(t *testing.T) {
	f := NewEgressFilter(nil)
	assert.False(t, f.HasRules())

	d := f.AllowHost("anything.com")
	assert.True(t, d.Allowed)
}

func TestEgressFilter_ExactDomain(t *testing.T) {
	f := NewEgressFilter([]EgressRule{
		{Hosts: []string{"api.github.com"}},
	})

	d := f.AllowHost("api.github.com")
	assert.True(t, d.Allowed)

	d = f.AllowHost("evil.com")
	assert.False(t, d.Allowed) // implicit deny
}

func TestEgressFilter_WildcardDomain(t *testing.T) {
	f := NewEgressFilter([]EgressRule{
		{Hosts: []string{"*.github.com"}},
	})

	assert.True(t, f.AllowHost("api.github.com").Allowed)
	assert.True(t, f.AllowHost("foo.bar.github.com").Allowed)
	assert.True(t, f.AllowHost("github.com").Allowed)
	assert.False(t, f.AllowHost("evil.com").Allowed)
}

func TestEgressFilter_CIDR(t *testing.T) {
	f := NewEgressFilter([]EgressRule{
		{Hosts: []string{"10.0.0.0/8"}},
	})

	assert.True(t, f.AllowHost("10.1.2.3").Allowed)
	assert.False(t, f.AllowHost("192.168.1.1").Allowed)
	assert.False(t, f.AllowHost("api.github.com").Allowed)
}

func TestEgressFilter_DenyRule(t *testing.T) {
	f := NewEgressFilter([]EgressRule{
		{Hosts: []string{"evil.com"}, Deny: true},
		{Hosts: []string{"*"}},
	})

	assert.False(t, f.AllowHost("evil.com").Allowed)
	assert.True(t, f.AllowHost("good.com").Allowed)
}

func TestEgressFilter_FirstMatchWins(t *testing.T) {
	f := NewEgressFilter([]EgressRule{
		{Hosts: []string{"*.github.com"}, Deny: true},
		{Hosts: []string{"api.github.com"}}, // won't be reached
	})

	d := f.AllowHost("api.github.com")
	assert.False(t, d.Allowed)
	assert.Equal(t, 0, d.RuleIndex)
}

func TestEgressFilter_CatchAll(t *testing.T) {
	f := NewEgressFilter([]EgressRule{
		{Hosts: []string{"api.github.com"}, Headers: map[string]string{"Auth": "token"}},
		{Hosts: []string{"*"}},
	})

	// Specific domain
	d := f.AllowHost("api.github.com")
	assert.True(t, d.Allowed)
	assert.NotNil(t, d.Rule)
	assert.Equal(t, "token", d.Rule.Headers["Auth"])

	// Catch-all
	d = f.AllowHost("random.io")
	assert.True(t, d.Allowed)
	assert.Equal(t, 1, d.RuleIndex)
}

func TestEgressFilter_AllowPath(t *testing.T) {
	rule := &EgressRule{
		Hosts:     []string{"api.github.com"},
		DenyPaths: []string{"DELETE /repos/*", "/admin/*"},
	}

	f := NewEgressFilter([]EgressRule{*rule})

	// DELETE /repos/foo — blocked
	assert.False(t, f.AllowPath(rule, "DELETE", "/repos/foo"))

	// GET /repos/foo — allowed (method doesn't match)
	assert.True(t, f.AllowPath(rule, "GET", "/repos/foo"))

	// Any method to /admin/users — blocked
	assert.False(t, f.AllowPath(rule, "GET", "/admin/users"))
	assert.False(t, f.AllowPath(rule, "POST", "/admin/settings"))

	// Normal path — allowed
	assert.True(t, f.AllowPath(rule, "GET", "/repos"))
	assert.True(t, f.AllowPath(rule, "GET", "/user/repos"))
}

func TestEgressFilter_NilRule_AllowPath(t *testing.T) {
	f := NewEgressFilter(nil)
	assert.True(t, f.AllowPath(nil, "GET", "/anything"))
}

func TestMatchHostPattern_EdgeCases(t *testing.T) {
	// Empty pattern
	assert.False(t, matchHostPattern("", "foo.com"))

	// Pattern with invalid glob character
	assert.False(t, matchHostPattern("[invalid", "foo.com"))
}
