package plugin

import (
	"fmt"
	"strconv"
	"strings"
)

// Requirement represents a parsed dependency constraint like "agent-docker>=1.0.0".
type Requirement struct {
	Name    string // plugin name
	Op      string // "", ">=", ">", "=", "~>"
	Version string // version string (without leading "v")
}

// ParseRequirement parses a requirement string into a Requirement.
// Supported formats: "name", "name>=1.0.0", "name>1.0.0", "name=1.0.0", "name~>1.2".
func ParseRequirement(s string) (Requirement, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Requirement{}, fmt.Errorf("empty requirement")
	}

	// Try operators in order of length (longest first to avoid partial matches).
	for _, op := range []string{">=", "~>", ">", "="} {
		if idx := strings.Index(s, op); idx > 0 {
			name := strings.TrimSpace(s[:idx])
			ver := strings.TrimSpace(s[idx+len(op):])
			if name == "" || ver == "" {
				return Requirement{}, fmt.Errorf("invalid requirement %q: empty name or version", s)
			}
			ver = strings.TrimPrefix(ver, "v")
			if _, err := parseVersion(ver); err != nil {
				return Requirement{}, fmt.Errorf("invalid version in requirement %q: %w", s, err)
			}
			return Requirement{Name: name, Op: op, Version: ver}, nil
		}
	}

	// No operator — just a name, any version.
	return Requirement{Name: s}, nil
}

// Satisfied reports whether the given version satisfies this requirement.
// An empty constraint (no operator) is always satisfied.
// A plugin without a declared version satisfies any constraint (backward compatible).
func (r Requirement) Satisfied(version string) bool {
	if r.Op == "" {
		return true // no constraint
	}
	if version == "" {
		return true // plugin has no version declared — always pass
	}
	version = strings.TrimPrefix(version, "v")

	have, err := parseVersion(version)
	if err != nil {
		return false
	}
	want, err := parseVersion(r.Version)
	if err != nil {
		return false
	}

	cmp := compareVersions(have, want)
	switch r.Op {
	case ">=":
		return cmp >= 0
	case ">":
		return cmp > 0
	case "=":
		return cmp == 0
	case "~>":
		// Pessimistic constraint: >=want AND <next major/minor bump.
		// ~>1.2.0 means >=1.2.0, <1.3.0
		// ~>1.2 means >=1.2.0, <2.0.0
		if cmp < 0 {
			return false
		}
		upper := bumpVersion(want)
		return compareVersions(have, upper) < 0
	default:
		return false
	}
}

// semver is a parsed major.minor.patch triple.
type semver struct {
	major, minor, patch int
}

func parseVersion(s string) (semver, error) {
	s = strings.TrimPrefix(s, "v")
	parts := strings.SplitN(s, ".", 3)
	var v semver
	var err error

	if len(parts) >= 1 {
		v.major, err = strconv.Atoi(parts[0])
		if err != nil {
			return v, fmt.Errorf("invalid major: %w", err)
		}
	}
	if len(parts) >= 2 {
		v.minor, err = strconv.Atoi(parts[1])
		if err != nil {
			return v, fmt.Errorf("invalid minor: %w", err)
		}
	}
	if len(parts) >= 3 {
		// Strip pre-release/build metadata for comparison.
		patchStr := parts[2]
		if idx := strings.IndexAny(patchStr, "-+"); idx >= 0 {
			patchStr = patchStr[:idx]
		}
		v.patch, err = strconv.Atoi(patchStr)
		if err != nil {
			return v, fmt.Errorf("invalid patch: %w", err)
		}
	}
	return v, nil
}

func compareVersions(a, b semver) int {
	if a.major != b.major {
		return a.major - b.major
	}
	if a.minor != b.minor {
		return a.minor - b.minor
	}
	return a.patch - b.patch
}

// bumpVersion increments the second-to-last specified component.
// For 3-part versions (1.2.3): bumps minor → 1.3.0
// For 2-part versions (1.2): bumps major → 2.0.0
func bumpVersion(v semver) semver {
	// ponytail: always treat as 3-part, bump minor. ~>1.2 parses as 1.2.0.
	return semver{major: v.major, minor: v.minor + 1, patch: 0}
}
