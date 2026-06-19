package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Policy defines the security rules for container creation.
type Policy struct {
	AllowedImages       []string
	MaxContainers       int
	AllowBuild          bool
	BuiltImages         map[string]bool
	AllowedCapabilities []string
}

// CreateRequest is the subset of Docker container create fields we validate.
type CreateRequest struct {
	Image       string
	Privileged  bool
	NetworkMode string
	CapAdd      []string
	PidMode     string
	IpcMode     string
	Binds       []string
}

// ImageAllowed checks if the image matches any pattern in the allowlist.
func (p *Policy) ImageAllowed(image string) bool {
	// Normalize: strip default registry/library prefix
	normalized := normalizeImage(image)

	// When AllowBuild is enabled, auto-allow buildkit images
	if p.AllowBuild && matchImage("moby/buildkit:*", normalized) {
		return true
	}

	// Check if locally built
	if p.BuiltImages != nil && (p.BuiltImages[image] || p.BuiltImages[normalized]) {
		return true
	}

	for _, pattern := range p.AllowedImages {
		if matchImage(pattern, normalized) {
			return true
		}
		// Also try matching the raw image (in case user specifies full paths in allowlist)
		if matchImage(pattern, image) {
			return true
		}
	}
	return false
}

// ValidateCreate checks a container create request against all policy rules.
func (p *Policy) ValidateCreate(req *CreateRequest, currentCount int) error {
	if !p.ImageAllowed(req.Image) {
		return &PolicyError{Code: 403, Message: fmt.Sprintf("image %q not in allowlist", req.Image)}
	}
	if req.Privileged {
		// Allow privileged for BuildKit builder when builds are enabled
		if !(p.AllowBuild && matchImage("moby/buildkit:*", normalizeImage(req.Image))) {
			return &PolicyError{Code: 403, Message: "privileged mode is not allowed"}
		}
	}
	if req.NetworkMode == "host" {
		return &PolicyError{Code: 403, Message: "host network mode is not allowed"}
	}
	if len(req.CapAdd) > 0 {
		if len(p.AllowedCapabilities) == 0 {
			return &PolicyError{Code: 403, Message: "adding capabilities is not allowed"}
		}
		for _, cap := range req.CapAdd {
			if !p.capAllowed(cap) {
				return &PolicyError{Code: 403, Message: fmt.Sprintf("capability %q is not allowed", cap)}
			}
		}
	}
	if req.PidMode == "host" {
		return &PolicyError{Code: 403, Message: "host PID mode is not allowed"}
	}
	if req.IpcMode == "host" {
		return &PolicyError{Code: 403, Message: "host IPC mode is not allowed"}
	}
	for _, bind := range req.Binds {
		src := strings.SplitN(bind, ":", 2)[0]
		if strings.HasPrefix(src, "/") {
			return &PolicyError{Code: 403, Message: fmt.Sprintf("host bind mount %q is not allowed", src)}
		}
	}
	if currentCount >= p.MaxContainers {
		return &PolicyError{Code: 429, Message: fmt.Sprintf("maximum container limit (%d) reached", p.MaxContainers)}
	}
	return nil
}

// PolicyError represents a policy violation with an HTTP status code.
type PolicyError struct {
	Code    int
	Message string
}

func (e *PolicyError) Error() string {
	return e.Message
}

// matchImage checks if an image string matches a glob pattern.
func matchImage(pattern, image string) bool {
	matched, err := filepath.Match(pattern, image)
	if err != nil {
		return false
	}
	return matched
}

// capAllowed checks if a capability is in the allowed list (case-insensitive, handles CAP_ prefix).
func (p *Policy) capAllowed(cap string) bool {
	normalized := strings.TrimPrefix(strings.ToUpper(cap), "CAP_")
	for _, allowed := range p.AllowedCapabilities {
		if strings.EqualFold(strings.TrimPrefix(allowed, "CAP_"), normalized) {
			return true
		}
	}
	return false
}

// normalizeImage strips the default Docker registry prefix.
// "docker.io/library/alpine:latest" → "alpine:latest"
// "docker.io/myuser/myimage:tag" → "myuser/myimage:tag"
func normalizeImage(image string) string {
	image = strings.TrimPrefix(image, "docker.io/")
	image = strings.TrimPrefix(image, "library/")
	return image
}
