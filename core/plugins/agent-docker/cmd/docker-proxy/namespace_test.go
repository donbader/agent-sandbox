package main

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNameTranslator_Namespace(t *testing.T) {
	nt := NewNameTranslator("box123")
	assert.Equal(t, "box123-web", nt.Namespace(KindContainer, "web"))
	assert.Equal(t, "box123-mynet", nt.Namespace(KindNetwork, "mynet"))
	assert.Equal(t, "box123-data", nt.Namespace(KindVolume, "data"))
}

func TestNameTranslator_Namespace_ReservedNetworks(t *testing.T) {
	nt := NewNameTranslator("box123")
	// Docker built-in networks must pass through unchanged.
	assert.Equal(t, "bridge", nt.Namespace(KindNetwork, "bridge"))
	assert.Equal(t, "host", nt.Namespace(KindNetwork, "host"))
	assert.Equal(t, "none", nt.Namespace(KindNetwork, "none"))
}

func TestNameTranslator_Namespace_ReservedOnlyForNetworks(t *testing.T) {
	nt := NewNameTranslator("box123")
	// "bridge" as a container or volume name is not reserved — namespace it.
	assert.Equal(t, "box123-bridge", nt.Namespace(KindContainer, "bridge"))
	assert.Equal(t, "box123-bridge", nt.Namespace(KindVolume, "bridge"))
}

func TestNameTranslator_Resolve_ByUserName(t *testing.T) {
	nt := NewNameTranslator("box123")
	nt.Track(KindContainer, "web", "box123-web")
	assert.Equal(t, "box123-web", nt.Resolve(KindContainer, "web"))
}

func TestNameTranslator_Resolve_ByRealName(t *testing.T) {
	nt := NewNameTranslator("box123")
	nt.Track(KindContainer, "web", "box123-web")
	// Resolving by real name returns it unchanged (caller already has the real name).
	assert.Equal(t, "box123-web", nt.Resolve(KindContainer, "box123-web"))
}

func TestNameTranslator_Resolve_Unknown(t *testing.T) {
	nt := NewNameTranslator("box123")
	assert.Equal(t, "", nt.Resolve(KindContainer, "unknown"))
	assert.Equal(t, "", nt.Resolve(KindNetwork, "mynet"))
}

func TestNameTranslator_UserName(t *testing.T) {
	nt := NewNameTranslator("box123")
	nt.Track(KindNetwork, "mynet", "box123-mynet")
	assert.Equal(t, "mynet", nt.UserName(KindNetwork, "box123-mynet"))
}

func TestNameTranslator_UserName_Unknown(t *testing.T) {
	nt := NewNameTranslator("box123")
	assert.Equal(t, "", nt.UserName(KindNetwork, "box123-ghost"))
}

func TestNameTranslator_Untrack_ByUserName(t *testing.T) {
	nt := NewNameTranslator("box123")
	nt.Track(KindContainer, "web", "box123-web")
	nt.Untrack(KindContainer, "web")
	assert.Equal(t, "", nt.Resolve(KindContainer, "web"))
	assert.Equal(t, "", nt.Resolve(KindContainer, "box123-web"))
	assert.Equal(t, "", nt.UserName(KindContainer, "box123-web"))
}

func TestNameTranslator_Untrack_ByRealName(t *testing.T) {
	nt := NewNameTranslator("box123")
	nt.Track(KindContainer, "web", "box123-web")
	nt.Untrack(KindContainer, "box123-web")
	assert.Equal(t, "", nt.Resolve(KindContainer, "web"))
	assert.Equal(t, "", nt.Resolve(KindContainer, "box123-web"))
}

func TestNameTranslator_Untrack_Unknown(t *testing.T) {
	nt := NewNameTranslator("box123")
	// Should not panic on unknown ref.
	assert.NotPanics(t, func() { nt.Untrack(KindContainer, "ghost") })
}

func TestNameTranslator_Track_EmptyUserName_NoOp(t *testing.T) {
	nt := NewNameTranslator("box123")
	nt.Track(KindContainer, "", "box123-abc")
	// No forward entry → can't resolve by user name (empty).
	// Crucially, "" should not pollute the map.
	assert.Equal(t, "", nt.UserName(KindContainer, "box123-abc"))
}

func TestNameTranslator_KindsAreIndependent(t *testing.T) {
	nt := NewNameTranslator("box123")
	nt.Track(KindContainer, "web", "box123-web-c")
	nt.Track(KindNetwork, "web", "box123-web-n")
	nt.Track(KindVolume, "web", "box123-web-v")

	assert.Equal(t, "box123-web-c", nt.Resolve(KindContainer, "web"))
	assert.Equal(t, "box123-web-n", nt.Resolve(KindNetwork, "web"))
	assert.Equal(t, "box123-web-v", nt.Resolve(KindVolume, "web"))

	// Untracking from one Kind does not affect others.
	nt.Untrack(KindContainer, "web")
	assert.Equal(t, "", nt.Resolve(KindContainer, "web"))
	assert.Equal(t, "box123-web-n", nt.Resolve(KindNetwork, "web"))
	assert.Equal(t, "box123-web-v", nt.Resolve(KindVolume, "web"))
}

func TestNameTranslator_TranslateNames(t *testing.T) {
	nt := NewNameTranslator("box123")
	nt.Track(KindContainer, "web", "box123-web")

	body := []byte(`[{"Id":"abc","Names":["/box123-web"],"Image":"nginx"}]`)
	got := nt.TranslateNames(KindContainer, body)
	assert.Equal(t, `[{"Id":"abc","Names":["/web"],"Image":"nginx"}]`, string(got))
}

func TestNameTranslator_TranslateNames_MultipleReplacements(t *testing.T) {
	nt := NewNameTranslator("box123")
	nt.Track(KindNetwork, "frontend", "box123-frontend")
	nt.Track(KindNetwork, "backend", "box123-backend")

	body := []byte(`[{"Name":"box123-frontend"},{"Name":"box123-backend"}]`)
	got := nt.TranslateNames(KindNetwork, body)
	assert.Contains(t, string(got), `"Name":"frontend"`)
	assert.Contains(t, string(got), `"Name":"backend"`)
	assert.NotContains(t, string(got), "box123-")
}

func TestNameTranslator_TranslateNames_NoTracked(t *testing.T) {
	nt := NewNameTranslator("box123")
	body := []byte(`[{"Name":"box123-web"}]`)
	got := nt.TranslateNames(KindContainer, body)
	// No tracked names → body unchanged.
	assert.Equal(t, string(body), string(got))
}

func TestNameTranslator_TranslateNames_WrongKind(t *testing.T) {
	nt := NewNameTranslator("box123")
	nt.Track(KindContainer, "web", "box123-web")

	body := []byte(`{"Name":"box123-web"}`)
	// Translating with KindNetwork should not replace container names.
	got := nt.TranslateNames(KindNetwork, body)
	assert.Equal(t, string(body), string(got))
}

func TestNameTranslator_Concurrent(t *testing.T) {
	nt := NewNameTranslator("box123")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("svc%d", i)
			real := nt.Namespace(KindContainer, name)
			nt.Track(KindContainer, name, real)
			_ = nt.Resolve(KindContainer, name)
			_ = nt.UserName(KindContainer, real)
			nt.Untrack(KindContainer, name)
		}(i)
	}
	wg.Wait()
}
