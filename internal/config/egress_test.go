package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchHost_ExactDomain(t *testing.T) {
	rules := []EgressRule{
		{Hosts: []string{"api.github.com"}},
	}
	m := MatchHost(rules, "api.github.com")
	assert.True(t, m.Matched)
	assert.False(t, m.Denied)

	m = MatchHost(rules, "evil.com")
	assert.False(t, m.Matched)
}

func TestMatchHost_WildcardDomain(t *testing.T) {
	rules := []EgressRule{
		{Hosts: []string{"*.github.com"}},
	}

	tests := []struct {
		host    string
		matched bool
	}{
		{"api.github.com", true},
		{"foo.bar.github.com", true},
		{"github.com", true},
		{"notgithub.com", false},
		{"evil.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			m := MatchHost(rules, tt.host)
			assert.Equal(t, tt.matched, m.Matched, "host=%s", tt.host)
		})
	}
}

func TestMatchHost_CIDR(t *testing.T) {
	rules := []EgressRule{
		{Hosts: []string{"10.0.0.0/8"}},
	}

	m := MatchHost(rules, "10.1.2.3")
	assert.True(t, m.Matched)

	m = MatchHost(rules, "192.168.1.1")
	assert.False(t, m.Matched)

	// Non-IP against CIDR should not match
	m = MatchHost(rules, "api.github.com")
	assert.False(t, m.Matched)
}

func TestMatchHost_CatchAll(t *testing.T) {
	rules := []EgressRule{
		{Hosts: []string{"evil.com"}, Deny: true},
		{Hosts: []string{"*"}},
	}

	m := MatchHost(rules, "evil.com")
	assert.True(t, m.Matched)
	assert.True(t, m.Denied)

	m = MatchHost(rules, "anything.else.com")
	assert.True(t, m.Matched)
	assert.False(t, m.Denied)
}

func TestMatchHost_FirstMatchWins(t *testing.T) {
	rules := []EgressRule{
		{Hosts: []string{"*.github.com"}, Deny: true},
		{Hosts: []string{"api.github.com"}}, // allow — but won't be reached
	}

	m := MatchHost(rules, "api.github.com")
	assert.True(t, m.Matched)
	assert.True(t, m.Denied)
	assert.Equal(t, 0, m.RuleIndex)
}

func TestMatchHost_ImplicitDeny(t *testing.T) {
	rules := []EgressRule{
		{Hosts: []string{"api.github.com"}},
	}

	// Not in any rule → implicit deny
	m := MatchHost(rules, "evil.com")
	assert.False(t, m.Matched)
}

func TestMatchPath_SimpleGlob(t *testing.T) {
	deny := []string{"/repos/*/delete"}

	assert.True(t, MatchPath(deny, "DELETE", "/repos/foo/delete"))
	assert.False(t, MatchPath(deny, "GET", "/repos/foo/issues"))
}

func TestMatchPath_MethodPrefix(t *testing.T) {
	deny := []string{"DELETE /repos/*"}

	assert.True(t, MatchPath(deny, "DELETE", "/repos/foo"))
	assert.False(t, MatchPath(deny, "GET", "/repos/foo"))
}

func TestMatchPath_NoMethod_MatchesAll(t *testing.T) {
	deny := []string{"/v1/fine_tuning/*"}

	assert.True(t, MatchPath(deny, "GET", "/v1/fine_tuning/jobs"))
	assert.True(t, MatchPath(deny, "POST", "/v1/fine_tuning/jobs"))
	assert.False(t, MatchPath(deny, "GET", "/v1/chat/completions"))
}

func TestMatchPath_PrefixWithStar(t *testing.T) {
	deny := []string{"/admin/*"}

	assert.True(t, MatchPath(deny, "GET", "/admin/users"))
	assert.True(t, MatchPath(deny, "POST", "/admin/settings/reset"))
	assert.False(t, MatchPath(deny, "GET", "/api/users"))
}

func TestValidateEgressRules(t *testing.T) {
	t.Run("valid rules", func(t *testing.T) {
		rules := []EgressRule{
			{Hosts: []string{"api.github.com"}, Headers: map[string]string{"Authorization": "Bearer ${PAT}"}},
			{Hosts: []string{"evil.com"}, Deny: true},
			{Hosts: []string{"*"}},
		}
		errs := ValidateEgressRules(rules)
		assert.Empty(t, errs)
	})

	t.Run("empty hosts", func(t *testing.T) {
		rules := []EgressRule{
			{Hosts: []string{}},
		}
		errs := ValidateEgressRules(rules)
		assert.Len(t, errs, 1)
		assert.Contains(t, errs[0], "hosts is required")
	})

	t.Run("deny with headers", func(t *testing.T) {
		rules := []EgressRule{
			{Hosts: []string{"evil.com"}, Deny: true, Headers: map[string]string{"X-Foo": "bar"}},
		}
		errs := ValidateEgressRules(rules)
		assert.Len(t, errs, 1)
		assert.Contains(t, errs[0], "cannot have both deny: true and headers")
	})

	t.Run("deny with deny_paths", func(t *testing.T) {
		rules := []EgressRule{
			{Hosts: []string{"evil.com"}, Deny: true, DenyPaths: []string{"/foo"}},
		}
		errs := ValidateEgressRules(rules)
		assert.Len(t, errs, 1)
		assert.Contains(t, errs[0], "cannot have both deny: true and deny_paths")
	})
}

func TestMigrateServicesToEgress(t *testing.T) {
	services := []GatewayServiceEntry{
		{
			URL: "https://api.github.com",
			Headers: map[string]string{
				"Authorization": "Bearer ${GITHUB_PAT}",
			},
		},
		{
			URL: "sidecar:8080",
		},
	}

	rules := MigrateServicesToEgress(services)

	assert.Len(t, rules, 3) // 2 services + catch-all
	assert.Equal(t, []string{"api.github.com"}, rules[0].Hosts)
	assert.Equal(t, "Bearer ${GITHUB_PAT}", rules[0].Headers["Authorization"])
	assert.Equal(t, []string{"sidecar"}, rules[1].Hosts)
	assert.Equal(t, []string{"*"}, rules[2].Hosts) // catch-all
}

func TestEgressRule_NeedsMITM(t *testing.T) {
	assert.True(t, (&EgressRule{Headers: map[string]string{"X": "Y"}}).NeedsMITM())
	assert.True(t, (&EgressRule{DenyPaths: []string{"/foo"}}).NeedsMITM())
	assert.False(t, (&EgressRule{Hosts: []string{"*"}}).NeedsMITM())
	assert.False(t, (&EgressRule{Deny: true}).NeedsMITM())
}

func TestHasLegacyServices(t *testing.T) {
	t.Run("has legacy", func(t *testing.T) {
		cfg := &Config{
			Gateway: GatewayConfig{
				Services: []GatewayServiceEntry{{URL: "https://api.example.com"}},
			},
		}
		assert.True(t, HasLegacyServices(cfg))
	})

	t.Run("has egress", func(t *testing.T) {
		cfg := &Config{
			Gateway: GatewayConfig{
				Egress: []EgressRule{{Hosts: []string{"*"}}},
			},
		}
		assert.False(t, HasLegacyServices(cfg))
	})

	t.Run("has both — not legacy", func(t *testing.T) {
		cfg := &Config{
			Gateway: GatewayConfig{
				Services: []GatewayServiceEntry{{URL: "https://api.example.com"}},
				Egress:   []EgressRule{{Hosts: []string{"*"}}},
			},
		}
		assert.False(t, HasLegacyServices(cfg))
	})
}
