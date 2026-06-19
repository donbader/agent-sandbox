package main

import (
	"bytes"
	"fmt"
	"sync"
)

// Kind identifies the type of Docker resource being namespaced.
type Kind int

const (
	KindContainer Kind = iota
	KindNetwork
	KindVolume
)

// reservedNames holds names that are never namespaced, keyed by Kind.
// These are Docker built-ins that must remain reachable by their canonical names.
var reservedNames = [3]map[string]bool{
	KindNetwork: {"bridge": true, "host": true, "none": true},
}

// NameTranslator manages bidirectional name mappings for Docker resources.
// It namespaces user-visible names with a sandbox prefix and translates
// in both directions — user→real on requests, real→user on responses.
// All methods are safe for concurrent use.
type NameTranslator struct {
	sandboxID string
	mu        sync.RWMutex
	forward   [3]map[string]string // userName → realName, per Kind
	reverse   [3]map[string]string // realName → userName, per Kind
}

// NewNameTranslator creates a translator for the given sandbox ID.
func NewNameTranslator(sandboxID string) *NameTranslator {
	nt := &NameTranslator{sandboxID: sandboxID}
	for i := range nt.forward {
		nt.forward[i] = make(map[string]string)
		nt.reverse[i] = make(map[string]string)
	}
	return nt
}

// Namespace returns the real (sandbox-prefixed) name for a user-provided name.
// Reserved names (e.g. bridge, host, none for networks) are returned unchanged.
// Callers are responsible for ensuring userName is non-empty.
func (nt *NameTranslator) Namespace(kind Kind, userName string) string {
	if reservedNames[kind][userName] {
		return userName
	}
	return fmt.Sprintf("%s-%s", nt.sandboxID, userName)
}

// Track registers a user name → real name mapping for later resolution.
// Calling Track with an empty userName is a no-op.
func (nt *NameTranslator) Track(kind Kind, userName, realName string) {
	if userName == "" {
		return
	}
	nt.mu.Lock()
	defer nt.mu.Unlock()
	nt.forward[kind][userName] = realName
	nt.reverse[kind][realName] = userName
}

// Untrack removes the mapping for ref, which may be a user name or a real name.
func (nt *NameTranslator) Untrack(kind Kind, ref string) {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	if real, ok := nt.forward[kind][ref]; ok {
		delete(nt.reverse[kind], real)
		delete(nt.forward[kind], ref)
		return
	}
	if user, ok := nt.reverse[kind][ref]; ok {
		delete(nt.forward[kind], user)
		delete(nt.reverse[kind], ref)
	}
}

// Resolve translates a reference to the real (sandbox-prefixed) name.
// Accepts either a user name (forward lookup) or a real name already tracked
// (reverse lookup, returns ref unchanged). Returns empty string if not tracked.
func (nt *NameTranslator) Resolve(kind Kind, ref string) string {
	nt.mu.RLock()
	defer nt.mu.RUnlock()
	if real, ok := nt.forward[kind][ref]; ok {
		return real
	}
	if _, ok := nt.reverse[kind][ref]; ok {
		return ref // already a real name we own
	}
	return ""
}

// UserName returns the user-visible name for a real (sandbox-prefixed) name.
// Returns empty string if not tracked.
func (nt *NameTranslator) UserName(kind Kind, realName string) string {
	nt.mu.RLock()
	defer nt.mu.RUnlock()
	return nt.reverse[kind][realName]
}

// TranslateNames replaces all real names with user names in body.
// Intended for stripping sandbox prefixes from Docker API responses before
// forwarding them to the client. Operates as a simple byte replacement so
// callers should apply it to complete, valid response bodies.
func (nt *NameTranslator) TranslateNames(kind Kind, body []byte) []byte {
	nt.mu.RLock()
	defer nt.mu.RUnlock()
	for real, user := range nt.reverse[kind] {
		body = bytes.ReplaceAll(body, []byte(real), []byte(user))
	}
	return body
}
