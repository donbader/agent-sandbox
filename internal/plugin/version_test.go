package plugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRequirement_NameOnly(t *testing.T) {
	r, err := ParseRequirement("agent-docker")
	require.NoError(t, err)
	assert.Equal(t, "agent-docker", r.Name)
	assert.Empty(t, r.Op)
	assert.Empty(t, r.Version)
}

func TestParseRequirement_GreaterEqual(t *testing.T) {
	r, err := ParseRequirement("agent-docker>=1.2.0")
	require.NoError(t, err)
	assert.Equal(t, "agent-docker", r.Name)
	assert.Equal(t, ">=", r.Op)
	assert.Equal(t, "1.2.0", r.Version)
}

func TestParseRequirement_GreaterThan(t *testing.T) {
	r, err := ParseRequirement("mcp-oauth>2.0.0")
	require.NoError(t, err)
	assert.Equal(t, "mcp-oauth", r.Name)
	assert.Equal(t, ">", r.Op)
	assert.Equal(t, "2.0.0", r.Version)
}

func TestParseRequirement_Exact(t *testing.T) {
	r, err := ParseRequirement("core=1.0.0")
	require.NoError(t, err)
	assert.Equal(t, "core", r.Name)
	assert.Equal(t, "=", r.Op)
	assert.Equal(t, "1.0.0", r.Version)
}

func TestParseRequirement_Pessimistic(t *testing.T) {
	r, err := ParseRequirement("tools~>1.3")
	require.NoError(t, err)
	assert.Equal(t, "tools", r.Name)
	assert.Equal(t, "~>", r.Op)
	assert.Equal(t, "1.3", r.Version)
}

func TestParseRequirement_WithVPrefix(t *testing.T) {
	r, err := ParseRequirement("foo>=v2.1.0")
	require.NoError(t, err)
	assert.Equal(t, "2.1.0", r.Version)
}

func TestParseRequirement_WithSpaces(t *testing.T) {
	r, err := ParseRequirement("  foo >= 1.0.0 ")
	require.NoError(t, err)
	assert.Equal(t, "foo", r.Name)
	assert.Equal(t, ">=", r.Op)
	assert.Equal(t, "1.0.0", r.Version)
}

func TestParseRequirement_Empty(t *testing.T) {
	_, err := ParseRequirement("")
	assert.Error(t, err)
}

func TestParseRequirement_InvalidVersion(t *testing.T) {
	_, err := ParseRequirement("foo>=abc")
	assert.Error(t, err)
}

func TestSatisfied_NoConstraint(t *testing.T) {
	r := Requirement{Name: "foo"}
	assert.True(t, r.Satisfied("1.0.0"))
	assert.True(t, r.Satisfied(""))
}

func TestSatisfied_NoVersionDeclared(t *testing.T) {
	r := Requirement{Name: "foo", Op: ">=", Version: "1.0.0"}
	// Plugin without version always satisfies (backward compat).
	assert.True(t, r.Satisfied(""))
}

func TestSatisfied_GreaterEqual(t *testing.T) {
	r := Requirement{Name: "foo", Op: ">=", Version: "1.2.0"}
	assert.True(t, r.Satisfied("1.2.0"))
	assert.True(t, r.Satisfied("1.3.0"))
	assert.True(t, r.Satisfied("2.0.0"))
	assert.False(t, r.Satisfied("1.1.9"))
	assert.False(t, r.Satisfied("0.9.0"))
}

func TestSatisfied_GreaterThan(t *testing.T) {
	r := Requirement{Name: "foo", Op: ">", Version: "1.0.0"}
	assert.True(t, r.Satisfied("1.0.1"))
	assert.True(t, r.Satisfied("2.0.0"))
	assert.False(t, r.Satisfied("1.0.0"))
	assert.False(t, r.Satisfied("0.9.9"))
}

func TestSatisfied_Exact(t *testing.T) {
	r := Requirement{Name: "foo", Op: "=", Version: "1.5.0"}
	assert.True(t, r.Satisfied("1.5.0"))
	assert.False(t, r.Satisfied("1.5.1"))
	assert.False(t, r.Satisfied("1.4.9"))
}

func TestSatisfied_Pessimistic(t *testing.T) {
	// ~>1.2.0 means >=1.2.0, <1.3.0
	r := Requirement{Name: "foo", Op: "~>", Version: "1.2.0"}
	assert.True(t, r.Satisfied("1.2.0"))
	assert.True(t, r.Satisfied("1.2.9"))
	assert.False(t, r.Satisfied("1.3.0"))
	assert.False(t, r.Satisfied("1.1.0"))
	assert.False(t, r.Satisfied("2.0.0"))
}

func TestSatisfied_PessimisticTwoPart(t *testing.T) {
	// ~>1.2 means >=1.2.0, <1.3.0
	r := Requirement{Name: "foo", Op: "~>", Version: "1.2"}
	assert.True(t, r.Satisfied("1.2.0"))
	assert.True(t, r.Satisfied("1.2.5"))
	assert.False(t, r.Satisfied("1.3.0"))
	assert.False(t, r.Satisfied("2.0.0"))
}
