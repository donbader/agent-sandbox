package plugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveOrder_NoDeps(t *testing.T) {
	a := &PluginDef{Name: "a"}
	b := &PluginDef{Name: "b"}
	idx := map[string]*PluginDef{"a": a, "b": b}

	sorted, err := ResolveOrder([]*PluginDef{a, b}, idx)
	require.NoError(t, err)
	assert.Len(t, sorted, 2)
}

func TestResolveOrder_LinearDeps(t *testing.T) {
	a := &PluginDef{Name: "a"}
	b := &PluginDef{Name: "b", Requires: []string{"a"}}
	c := &PluginDef{Name: "c", Requires: []string{"b"}}
	idx := map[string]*PluginDef{"a": a, "b": b, "c": c}

	sorted, err := ResolveOrder([]*PluginDef{c, b, a}, idx)
	require.NoError(t, err)
	require.Len(t, sorted, 3)

	// a before b, b before c
	posA, posB, posC := indexOf(sorted, a), indexOf(sorted, b), indexOf(sorted, c)
	assert.Less(t, posA, posB)
	assert.Less(t, posB, posC)
}

func TestResolveOrder_DiamondDeps(t *testing.T) {
	//   a
	//  / \
	// b   c
	//  \ /
	//   d
	a := &PluginDef{Name: "a"}
	b := &PluginDef{Name: "b", Requires: []string{"a"}}
	c := &PluginDef{Name: "c", Requires: []string{"a"}}
	d := &PluginDef{Name: "d", Requires: []string{"b", "c"}}
	idx := map[string]*PluginDef{"a": a, "b": b, "c": c, "d": d}

	sorted, err := ResolveOrder([]*PluginDef{d, c, b, a}, idx)
	require.NoError(t, err)
	require.Len(t, sorted, 4)

	posA := indexOf(sorted, a)
	posB := indexOf(sorted, b)
	posC := indexOf(sorted, c)
	posD := indexOf(sorted, d)
	assert.Less(t, posA, posB)
	assert.Less(t, posA, posC)
	assert.Less(t, posB, posD)
	assert.Less(t, posC, posD)
}

func TestResolveOrder_Cycle(t *testing.T) {
	a := &PluginDef{Name: "a", Requires: []string{"b"}}
	b := &PluginDef{Name: "b", Requires: []string{"a"}}
	idx := map[string]*PluginDef{"a": a, "b": b}

	_, err := ResolveOrder([]*PluginDef{a, b}, idx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestResolveOrder_MissingDepIgnored(t *testing.T) {
	// Missing deps are validated elsewhere — topo sort just ignores them.
	a := &PluginDef{Name: "a", Requires: []string{"nonexistent"}}
	idx := map[string]*PluginDef{"a": a}

	sorted, err := ResolveOrder([]*PluginDef{a}, idx)
	require.NoError(t, err)
	assert.Len(t, sorted, 1)
}

func TestResolveOrder_VersionConstraintInRequires(t *testing.T) {
	a := &PluginDef{Name: "a", Version: "2.0.0"}
	b := &PluginDef{Name: "b", Requires: []string{"a>=1.0.0"}}
	idx := map[string]*PluginDef{"a": a, "b": b}

	sorted, err := ResolveOrder([]*PluginDef{b, a}, idx)
	require.NoError(t, err)
	require.Len(t, sorted, 2)
	assert.Less(t, indexOf(sorted, a), indexOf(sorted, b))
}

func TestResolveOrder_Single(t *testing.T) {
	a := &PluginDef{Name: "a"}
	sorted, err := ResolveOrder([]*PluginDef{a}, nil)
	require.NoError(t, err)
	assert.Equal(t, []*PluginDef{a}, sorted)
}

func indexOf(slice []*PluginDef, target *PluginDef) int {
	for i, p := range slice {
		if p == target {
			return i
		}
	}
	return -1
}
