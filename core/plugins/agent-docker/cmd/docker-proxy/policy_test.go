package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestImageAllowed(t *testing.T) {
	p := &Policy{
		AllowedImages: []string{"node:20-*", "python:3.12-*", "postgres:16-*"},
	}

	assert.True(t, p.ImageAllowed("node:20-slim"))
	assert.True(t, p.ImageAllowed("node:20-alpine"))
	assert.True(t, p.ImageAllowed("python:3.12-slim"))
	assert.True(t, p.ImageAllowed("postgres:16-alpine"))
	assert.False(t, p.ImageAllowed("ubuntu:latest"))
	assert.False(t, p.ImageAllowed("node:18-slim"))
	assert.False(t, p.ImageAllowed("malicious/node:20-slim"))
}

func TestImageAllowed_Wildcard(t *testing.T) {
	p := &Policy{
		AllowedImages: []string{"node:*", "*/python:*"},
	}

	assert.True(t, p.ImageAllowed("node:20"))
	assert.True(t, p.ImageAllowed("node:latest"))
	// library/ is Docker's official image prefix — normalizes to node:20
	assert.True(t, p.ImageAllowed("library/node:20"))
	// Full registry path also normalizes
	assert.True(t, p.ImageAllowed("docker.io/library/node:20"))
	// Non-official registry image should NOT match
	assert.False(t, p.ImageAllowed("malicious/node:20"))
}

func TestValidateCreateRequest_Privileged(t *testing.T) {
	p := &Policy{AllowedImages: []string{"node:*"}, MaxContainers: 5}

	err := p.ValidateCreate(&CreateRequest{
		Image:      "node:20",
		Privileged: true,
	}, 0)
	assert.ErrorContains(t, err, "privileged")
}

func TestValidateCreateRequest_HostNetwork(t *testing.T) {
	p := &Policy{AllowedImages: []string{"node:*"}, MaxContainers: 5}

	err := p.ValidateCreate(&CreateRequest{
		Image:       "node:20",
		NetworkMode: "host",
	}, 0)
	assert.ErrorContains(t, err, "host network")
}

func TestValidateCreateRequest_CapAdd(t *testing.T) {
	p := &Policy{AllowedImages: []string{"node:*"}, MaxContainers: 5}

	err := p.ValidateCreate(&CreateRequest{
		Image:  "node:20",
		CapAdd: []string{"SYS_ADMIN"},
	}, 0)
	assert.ErrorContains(t, err, "capabilities")
}

func TestValidateCreateRequest_HostPID(t *testing.T) {
	p := &Policy{AllowedImages: []string{"node:*"}, MaxContainers: 5}

	err := p.ValidateCreate(&CreateRequest{
		Image:   "node:20",
		PidMode: "host",
	}, 0)
	assert.ErrorContains(t, err, "PID mode")
}

func TestValidateCreateRequest_HostIPC(t *testing.T) {
	p := &Policy{AllowedImages: []string{"node:*"}, MaxContainers: 5}

	err := p.ValidateCreate(&CreateRequest{
		Image:   "node:20",
		IpcMode: "host",
	}, 0)
	assert.ErrorContains(t, err, "IPC mode")
}

func TestValidateCreateRequest_HostBindMount(t *testing.T) {
	p := &Policy{AllowedImages: []string{"node:*"}, MaxContainers: 5}

	err := p.ValidateCreate(&CreateRequest{
		Image: "node:20",
		Binds: []string{"/etc/passwd:/etc/passwd"},
	}, 0)
	assert.ErrorContains(t, err, "bind mount")
}

func TestValidateCreateRequest_MaxContainers(t *testing.T) {
	p := &Policy{AllowedImages: []string{"node:*"}, MaxContainers: 3}

	err := p.ValidateCreate(&CreateRequest{Image: "node:20"}, 3)
	assert.ErrorContains(t, err, "maximum")
}

func TestValidateCreateRequest_Valid(t *testing.T) {
	p := &Policy{AllowedImages: []string{"node:*"}, MaxContainers: 5}

	err := p.ValidateCreate(&CreateRequest{Image: "node:20"}, 0)
	assert.NoError(t, err)
}

func TestValidateCreateRequest_RelativeBindMount(t *testing.T) {
	p := &Policy{AllowedImages: []string{"node:*"}, MaxContainers: 5}

	// Relative paths must be blocked
	for _, bind := range []string{"./foo:/data", "../etc:/data", "sub/path:/data"} {
		err := p.ValidateCreate(&CreateRequest{Image: "node:20", Binds: []string{bind}}, 0)
		assert.ErrorContains(t, err, "bind mount", "expected block for %q", bind)
	}

	// Named volume (no slash, no dot prefix) must be allowed
	err := p.ValidateCreate(&CreateRequest{Image: "node:20", Binds: []string{"myvolume:/data"}}, 0)
	assert.NoError(t, err)
}
