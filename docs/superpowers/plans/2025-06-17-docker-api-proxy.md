# Docker API Proxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a policy-enforced Docker API proxy sidecar that lets agents spawn containers safely.

**Architecture:** Plugin (`@builtin/agent-docker`) contributes a sidecar Go binary that proxies Docker API calls from the agent to the Docker daemon, enforcing image allowlists, resource limits, namespace isolation, and endpoint restrictions.

**Tech Stack:** Go (proxy binary), Docker Engine API (unix socket), plugin system (YAML)

**Depends on:** `2025-06-17-egress-hardening.md` (sandbox network must be `internal: true`)

---

### Task 1: Scaffold the Docker Proxy Binary

**Files:**
- Create: `core/plugins/agent-docker/cmd/docker-proxy/main.go`
- Create: `core/plugins/agent-docker/cmd/docker-proxy/Dockerfile`

- [ ] **Step 1: Create main.go skeleton**

```go
package main

import (
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)
	if os.Getenv("LOG_LEVEL") == "debug" {
		level.Set(slog.LevelDebug)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	cfg, err := loadConfigFromEnv()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	proxy, err := NewDockerProxy(cfg)
	if err != nil {
		slog.Error("create proxy", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:    ":2375",
		Handler: proxy,
	}

	go func() {
		slog.Info("docker proxy listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown, then cleanup
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	slog.Info("shutting down, cleaning up containers")
	proxy.Cleanup()
}
```

- [ ] **Step 2: Create Dockerfile**

```dockerfile
FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /docker-proxy ./core/plugins/agent-docker/cmd/docker-proxy/

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /docker-proxy /usr/local/bin/docker-proxy
ENTRYPOINT ["docker-proxy"]
```

- [ ] **Step 3: Verify it compiles (will fail — missing functions)**

Run: `go build ./core/plugins/agent-docker/cmd/docker-proxy/`
Expected: FAIL (missing `loadConfigFromEnv`, `NewDockerProxy`)

- [ ] **Step 4: Commit scaffold**

```bash
git add core/plugins/agent-docker/
git commit -m "feat(agent-docker): scaffold docker proxy binary"
```

---

### Task 2: Config Loading from Environment

**Files:**
- Create: `core/plugins/agent-docker/cmd/docker-proxy/config.go`
- Create: `core/plugins/agent-docker/cmd/docker-proxy/config_test.go`

- [ ] **Step 1: Write failing test for config parsing**

```go
package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigFromEnv(t *testing.T) {
	// System-injected env vars (from generator)
	t.Setenv("SANDBOX_ID", "my-project-coder")
	t.Setenv("SANDBOX_NETWORK", "my-project_sandbox")
	t.Setenv("AGENT_NAME", "coder")
	// Plugin-contributed env vars
	t.Setenv("ALLOWED_IMAGES", `["node:20-*","python:3.12-*"]`)
	t.Setenv("MAX_CONTAINERS", "5")
	t.Setenv("MEMORY_LIMIT", "2g")
	t.Setenv("CPU_LIMIT", "2")
	t.Setenv("PID_LIMIT", "256")

	cfg, err := loadConfigFromEnv()
	require.NoError(t, err)

	assert.Equal(t, "my-project-coder", cfg.SandboxID)
	assert.Equal(t, "coder", cfg.AgentName)
	assert.Equal(t, "my-project_sandbox", cfg.NetworkName)
	assert.Equal(t, []string{"node:20-*", "python:3.12-*"}, cfg.AllowedImages)
	assert.Equal(t, 5, cfg.MaxContainers)
	assert.Equal(t, int64(2*1024*1024*1024), cfg.MemoryBytes)
	assert.Equal(t, int64(2000000000), cfg.NanoCPUs)
	assert.Equal(t, int64(256), cfg.PidsLimit)
}

func TestLoadConfigFromEnv_MissingRequired(t *testing.T) {
	// Clear all env
	t.Setenv("SANDBOX_ID", "")
	t.Setenv("AGENT_NAME", "")
	t.Setenv("ALLOWED_IMAGES", "")

	_, err := loadConfigFromEnv()
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/plugins/agent-docker/cmd/docker-proxy/ -run TestLoadConfigFromEnv -v`
Expected: FAIL (function not defined)

- [ ] **Step 3: Implement config.go**

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ProxyConfig holds the Docker proxy configuration.
type ProxyConfig struct {
	SandboxID     string
	AgentName     string
	NetworkName   string
	AllowedImages []string
	MaxContainers int
	MemoryBytes   int64
	NanoCPUs      int64
	PidsLimit     int64
}

func loadConfigFromEnv() (*ProxyConfig, error) {
	cfg := &ProxyConfig{}

	cfg.SandboxID = os.Getenv("SANDBOX_ID")
	if cfg.SandboxID == "" {
		return nil, fmt.Errorf("SANDBOX_ID is required")
	}

	cfg.AgentName = os.Getenv("AGENT_NAME")
	if cfg.AgentName == "" {
		return nil, fmt.Errorf("AGENT_NAME is required")
	}

	cfg.NetworkName = os.Getenv("SANDBOX_NETWORK")
	if cfg.NetworkName == "" {
		return nil, fmt.Errorf("SANDBOX_NETWORK is required")
	}

	imagesJSON := os.Getenv("ALLOWED_IMAGES")
	if imagesJSON == "" {
		return nil, fmt.Errorf("ALLOWED_IMAGES is required")
	}
	if err := json.Unmarshal([]byte(imagesJSON), &cfg.AllowedImages); err != nil {
		return nil, fmt.Errorf("parse ALLOWED_IMAGES: %w", err)
	}

	cfg.MaxContainers = envInt("MAX_CONTAINERS", 5)
	cfg.MemoryBytes = parseMemory(os.Getenv("MEMORY_LIMIT"))
	cfg.NanoCPUs = parseCPUs(os.Getenv("CPU_LIMIT"))
	cfg.PidsLimit = int64(envInt("PID_LIMIT", 256))

	return cfg, nil
}

func envInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

// parseMemory converts "2g", "512m" to bytes.
func parseMemory(s string) int64 {
	if s == "" {
		return 2 * 1024 * 1024 * 1024 // default 2GB
	}
	s = strings.TrimSpace(strings.ToLower(s))
	multiplier := int64(1)
	if strings.HasSuffix(s, "g") {
		multiplier = 1024 * 1024 * 1024
		s = s[:len(s)-1]
	} else if strings.HasSuffix(s, "m") {
		multiplier = 1024 * 1024
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 2 * 1024 * 1024 * 1024
	}
	return n * multiplier
}

// parseCPUs converts "2" to NanoCPUs (2000000000).
func parseCPUs(s string) int64 {
	if s == "" {
		return 2000000000 // default 2 CPUs
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 2000000000
	}
	return int64(f * 1e9)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./core/plugins/agent-docker/cmd/docker-proxy/ -run TestLoadConfigFromEnv -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add core/plugins/agent-docker/cmd/docker-proxy/config.go core/plugins/agent-docker/cmd/docker-proxy/config_test.go
git commit -m "feat(agent-docker): config loading from environment variables"
```

---

### Task 3: Policy Engine — Image Allowlist and Validation

**Files:**
- Create: `core/plugins/agent-docker/cmd/docker-proxy/policy.go`
- Create: `core/plugins/agent-docker/cmd/docker-proxy/policy_test.go`

- [ ] **Step 1: Write failing tests for policy**

```go
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
	assert.False(t, p.ImageAllowed("library/node:20"))
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./core/plugins/agent-docker/cmd/docker-proxy/ -run TestImage -v`
Expected: FAIL (types not defined)

- [ ] **Step 3: Implement policy.go**

```go
package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Policy defines the security rules for container creation.
type Policy struct {
	AllowedImages []string
	MaxContainers int
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
	for _, pattern := range p.AllowedImages {
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
		return &PolicyError{Code: 403, Message: "privileged mode is not allowed"}
	}
	if req.NetworkMode == "host" {
		return &PolicyError{Code: 403, Message: "host network mode is not allowed"}
	}
	if len(req.CapAdd) > 0 {
		return &PolicyError{Code: 403, Message: "adding capabilities is not allowed"}
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
// Patterns like "node:20-*" match "node:20-slim", "node:20-alpine", etc.
func matchImage(pattern, image string) bool {
	// filepath.Match handles * glob matching
	matched, err := filepath.Match(pattern, image)
	if err != nil {
		return false
	}
	return matched
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./core/plugins/agent-docker/cmd/docker-proxy/ -run "TestImage|TestValidate" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add core/plugins/agent-docker/cmd/docker-proxy/policy.go core/plugins/agent-docker/cmd/docker-proxy/policy_test.go
git commit -m "feat(agent-docker): policy engine — image allowlist and create validation"
```

---

### Task 4: Mutation Engine — Force Labels, Limits, Network, Names

**Files:**
- Create: `core/plugins/agent-docker/cmd/docker-proxy/mutate.go`
- Create: `core/plugins/agent-docker/cmd/docker-proxy/mutate_test.go`

- [ ] **Step 1: Write failing tests for mutation**

```go
package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMutateCreate(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:   "my-project-coder",
		AgentName:   "coder",
		NetworkName: "my-project_sandbox",
		MemoryBytes: 2 * 1024 * 1024 * 1024,
		NanoCPUs:    2000000000,
		PidsLimit:   256,
	}
	m := NewMutator(cfg)

	body := map[string]any{
		"Image": "node:20",
		"HostConfig": map[string]any{
			"NetworkMode": "bridge",
		},
	}

	m.MutateCreate(body, "my-app")

	// Check labels
	labels, _ := body["Labels"].(map[string]any)
	assert.Equal(t, "coder", labels["agent-sandbox.agent"])
	assert.Equal(t, "my-project-coder", labels["agent-sandbox.sandbox"])

	// Check host config
	hc, _ := body["HostConfig"].(map[string]any)
	assert.Equal(t, "my-project_sandbox", hc["NetworkMode"])
	assert.Equal(t, int64(2*1024*1024*1024), hc["Memory"])
	assert.Equal(t, int64(2000000000), hc["NanoCpus"])
	assert.Equal(t, int64(256), hc["PidsLimit"])
	rp, _ := hc["RestartPolicy"].(map[string]any)
	assert.Equal(t, "no", rp["Name"])
}

func TestMutateCreate_NamespaceContainerName(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:   "my-project-coder",
		AgentName:   "coder",
		NetworkName: "my-project_sandbox",
		MemoryBytes: 2 * 1024 * 1024 * 1024,
		NanoCPUs:    2000000000,
		PidsLimit:   256,
	}
	m := NewMutator(cfg)

	// With user-provided name
	name := m.NamespaceContainerName("my-postgres")
	assert.Equal(t, "my-project-coder-my-postgres", name)

	// Empty name gets random suffix
	name = m.NamespaceContainerName("")
	assert.True(t, len(name) > len("my-project-coder-"))
	assert.Contains(t, name, "my-project-coder-")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./core/plugins/agent-docker/cmd/docker-proxy/ -run TestMutate -v`
Expected: FAIL

- [ ] **Step 3: Implement mutate.go**

```go
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Mutator applies forced values to container create requests.
type Mutator struct {
	cfg *ProxyConfig
}

// NewMutator creates a mutator with the given config.
func NewMutator(cfg *ProxyConfig) *Mutator {
	return &Mutator{cfg: cfg}
}

// MutateCreate applies all forced mutations to a container create body.
func (m *Mutator) MutateCreate(body map[string]any, containerName string) {
	// Force labels
	labels, ok := body["Labels"].(map[string]any)
	if !ok || labels == nil {
		labels = map[string]any{}
	}
	labels["agent-sandbox.agent"] = m.cfg.AgentName
	labels["agent-sandbox.sandbox"] = m.cfg.SandboxID
	body["Labels"] = labels

	// Force HostConfig
	hc, ok := body["HostConfig"].(map[string]any)
	if !ok || hc == nil {
		hc = map[string]any{}
	}
	hc["NetworkMode"] = m.cfg.NetworkName
	hc["Memory"] = m.cfg.MemoryBytes
	hc["NanoCpus"] = m.cfg.NanoCPUs
	hc["PidsLimit"] = m.cfg.PidsLimit
	hc["RestartPolicy"] = map[string]any{"Name": "no"}
	body["HostConfig"] = hc
}

// NamespaceContainerName prefixes a container name with sandbox identity.
func (m *Mutator) NamespaceContainerName(name string) string {
	prefix := fmt.Sprintf("%s-", m.cfg.SandboxID)
	if name == "" {
		name = randomSuffix()
	}
	return prefix + name
}

func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./core/plugins/agent-docker/cmd/docker-proxy/ -run TestMutate -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add core/plugins/agent-docker/cmd/docker-proxy/mutate.go core/plugins/agent-docker/cmd/docker-proxy/mutate_test.go
git commit -m "feat(agent-docker): mutation engine — labels, limits, network, name namespacing"
```

---

// === SECTION: Task 5 ===

### Task 5: HTTP Proxy Handler — Request Routing and Endpoint Filtering

**Files:**
- Create: `core/plugins/agent-docker/cmd/docker-proxy/proxy.go`
- Create: `core/plugins/agent-docker/cmd/docker-proxy/proxy_test.go`

- [ ] **Step 1: Write failing test for endpoint routing**

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEndpointAllowed(t *testing.T) {
	cases := []struct {
		method string
		path   string
		allow  bool
	}{
		{"POST", "/containers/create", true},
		{"POST", "/containers/abc123/start", true},
		{"POST", "/containers/abc123/stop", true},
		{"POST", "/containers/abc123/kill", true},
		{"DELETE", "/containers/abc123", true},
		{"GET", "/containers/abc123/json", true},
		{"GET", "/containers/abc123/logs", true},
		{"GET", "/containers/json", true},
		{"POST", "/containers/abc123/exec", true},
		{"POST", "/exec/abc123/start", true},
		{"GET", "/images/json", true},
		{"POST", "/images/create", true},
		// Blocked
		{"GET", "/volumes", false},
		{"POST", "/volumes/create", false},
		{"GET", "/networks", false},
		{"GET", "/swarm", false},
		{"GET", "/secrets", false},
		{"GET", "/configs", false},
		{"GET", "/system/info", false},
		{"GET", "/unknown/endpoint", false},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			assert.Equal(t, tc.allow, isEndpointAllowed(tc.method, tc.path))
		})
	}
}

func TestDockerProxy_BlockedEndpoint(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	}
	proxy, _ := NewDockerProxy(cfg)

	req := httptest.NewRequest("GET", "/volumes", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./core/plugins/agent-docker/cmd/docker-proxy/ -run "TestEndpoint|TestDockerProxy_Blocked" -v`
Expected: FAIL

- [ ] **Step 3: Implement proxy.go**

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"
	"sync"
)

// DockerProxy is the HTTP handler that validates and forwards Docker API requests.
type DockerProxy struct {
	policy   *Policy
	mutator  *Mutator
	cfg      *ProxyConfig
	upstream *httputil.ReverseProxy
	mu       sync.Mutex
	tracked  map[string]bool // container IDs owned by this sandbox
}

// NewDockerProxy creates a new Docker API proxy.
func NewDockerProxy(cfg *ProxyConfig) (*DockerProxy, error) {
	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", "/var/run/docker.sock")
		},
	}
	upstream := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = "docker"
		},
		Transport: transport,
	}

	return &DockerProxy{
		policy: &Policy{
			AllowedImages: cfg.AllowedImages,
			MaxContainers: cfg.MaxContainers,
		},
		mutator:  NewMutator(cfg),
		cfg:      cfg,
		upstream: upstream,
		tracked:  make(map[string]bool),
	}, nil
}

func (dp *DockerProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isEndpointAllowed(r.Method, r.URL.Path) {
		writeError(w, http.StatusForbidden, "endpoint not allowed")
		return
	}

	// Route to specific handlers
	switch {
	case r.Method == "POST" && r.URL.Path == "/containers/create":
		dp.handleContainerCreate(w, r)
	case r.Method == "POST" && r.URL.Path == "/images/create":
		dp.handleImagePull(w, r)
	case r.Method == "GET" && r.URL.Path == "/containers/json":
		dp.handleContainerList(w, r)
	default:
		// For namespace-checked endpoints, verify ownership
		if id := extractContainerID(r.URL.Path); id != "" {
			if !dp.isOwned(id) {
				writeError(w, http.StatusNotFound, "container not found")
				return
			}
		}
		dp.upstream.ServeHTTP(w, r)
	}
}

// Cleanup stops and removes all tracked containers.
func (dp *DockerProxy) Cleanup() {
	// Implementation in cleanup.go
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

// isEndpointAllowed checks if a method+path combination is in the allowlist.
func isEndpointAllowed(method, path string) bool {
	for _, rule := range allowedEndpoints {
		if rule.method == method && rule.pattern.MatchString(path) {
			return true
		}
	}
	return false
}

type endpointRule struct {
	method  string
	pattern *regexp.Regexp
}

var allowedEndpoints = []endpointRule{
	{"POST", regexp.MustCompile(`^/containers/create$`)},
	{"POST", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+/start$`)},
	{"POST", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+/stop$`)},
	{"POST", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+/kill$`)},
	{"DELETE", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+$`)},
	{"GET", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+/json$`)},
	{"GET", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+/logs$`)},
	{"GET", regexp.MustCompile(`^/containers/json$`)},
	{"POST", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+/exec$`)},
	{"POST", regexp.MustCompile(`^/exec/[a-zA-Z0-9_.-]+/start$`)},
	{"GET", regexp.MustCompile(`^/images/json$`)},
	{"POST", regexp.MustCompile(`^/images/create$`)},
}

// extractContainerID pulls the container ID from paths like /containers/{id}/start
func extractContainerID(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "containers" && parts[1] != "json" && parts[1] != "create" {
		return parts[1]
	}
	return ""
}

func (dp *DockerProxy) isOwned(id string) bool {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	return dp.tracked[id]
}

func (dp *DockerProxy) trackContainer(id string) {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	dp.tracked[id] = true
}

func (dp *DockerProxy) untrackContainer(id string) {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	delete(dp.tracked, id)
}
```

Note: `handleContainerCreate`, `handleImagePull`, `handleContainerList` will be stubs that call `dp.upstream.ServeHTTP` for now — implemented in the next task.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./core/plugins/agent-docker/cmd/docker-proxy/ -run "TestEndpoint|TestDockerProxy_Blocked" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add core/plugins/agent-docker/cmd/docker-proxy/proxy.go core/plugins/agent-docker/cmd/docker-proxy/proxy_test.go
git commit -m "feat(agent-docker): HTTP proxy handler with endpoint allowlist"
```

---

### Task 6: Container Create, Image Pull, and List Handlers

**Files:**
- Modify: `core/plugins/agent-docker/cmd/docker-proxy/proxy.go`
- Modify: `core/plugins/agent-docker/cmd/docker-proxy/proxy_test.go`

- [ ] **Step 1: Write test for container create with policy violation**

```go
func TestDockerProxy_ContainerCreate_PolicyViolation(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	}
	proxy, _ := NewDockerProxy(cfg)

	// Request with disallowed image
	body := `{"Image": "ubuntu:latest", "HostConfig": {}}`
	req := httptest.NewRequest("POST", "/containers/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "not in allowlist")
}

func TestDockerProxy_ContainerCreate_Privileged(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	}
	proxy, _ := NewDockerProxy(cfg)

	body := `{"Image": "node:20", "HostConfig": {"Privileged": true}}`
	req := httptest.NewRequest("POST", "/containers/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "privileged")
}

func TestDockerProxy_ImagePull_Blocked(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	}
	proxy, _ := NewDockerProxy(cfg)

	req := httptest.NewRequest("POST", "/images/create?fromImage=ubuntu&tag=latest", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "not in allowlist")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./core/plugins/agent-docker/cmd/docker-proxy/ -run "TestDockerProxy_Container|TestDockerProxy_Image" -v`
Expected: FAIL (handler methods not implemented)

- [ ] **Step 3: Implement handleContainerCreate**

Add to `proxy.go`:

```go
func (dp *DockerProxy) handleContainerCreate(w http.ResponseWriter, r *http.Request) {
	// Read and parse body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	defer r.Body.Close()

	var body map[string]any
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Extract fields for validation
	createReq := extractCreateRequest(body)

	// Validate against policy
	dp.mu.Lock()
	currentCount := len(dp.tracked)
	dp.mu.Unlock()

	if err := dp.policy.ValidateCreate(createReq, currentCount); err != nil {
		if pe, ok := err.(*PolicyError); ok {
			writeError(w, pe.Code, pe.Message)
		} else {
			writeError(w, http.StatusForbidden, err.Error())
		}
		return
	}

	// Extract container name from query param
	containerName := r.URL.Query().Get("name")
	namespacedName := dp.mutator.NamespaceContainerName(containerName)

	// Mutate the request
	dp.mutator.MutateCreate(body, namespacedName)

	// Re-encode and forward
	mutatedBody, _ := json.Marshal(body)

	// Build new request with namespaced name in query
	newURL := *r.URL
	q := newURL.Query()
	q.Set("name", namespacedName)
	newURL.RawQuery = q.Encode()

	newReq, _ := http.NewRequest(r.Method, newURL.String(), io.NopCloser(strings.NewReader(string(mutatedBody))))
	newReq.Header = r.Header
	newReq.Header.Set("Content-Length", fmt.Sprintf("%d", len(mutatedBody)))

	// Capture response to extract container ID
	rec := httptest.NewRecorder()
	dp.upstream.ServeHTTP(rec, newReq)

	// Track the container if created successfully
	if rec.Code == http.StatusCreated {
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
			if id, ok := resp["Id"].(string); ok {
				dp.trackContainer(id)
				// Also track by name for lookup
				dp.trackContainer(namespacedName)
				slog.Info("container created", "id", id[:12], "name", namespacedName, "image", createReq.Image)
			}
		}
	}

	// Write response back to client
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

// extractCreateRequest pulls validation-relevant fields from a Docker create body.
func extractCreateRequest(body map[string]any) *CreateRequest {
	req := &CreateRequest{}

	if img, ok := body["Image"].(string); ok {
		req.Image = img
	}

	hc, _ := body["HostConfig"].(map[string]any)
	if hc != nil {
		if priv, ok := hc["Privileged"].(bool); ok {
			req.Privileged = priv
		}
		if nm, ok := hc["NetworkMode"].(string); ok {
			req.NetworkMode = nm
		}
		if caps, ok := hc["CapAdd"].([]any); ok {
			for _, c := range caps {
				if s, ok := c.(string); ok {
					req.CapAdd = append(req.CapAdd, s)
				}
			}
		}
		if pm, ok := hc["PidMode"].(string); ok {
			req.PidMode = pm
		}
		if im, ok := hc["IpcMode"].(string); ok {
			req.IpcMode = im
		}
		if binds, ok := hc["Binds"].([]any); ok {
			for _, b := range binds {
				if s, ok := b.(string); ok {
					req.Binds = append(req.Binds, s)
				}
			}
		}
	}

	return req
}
```

- [ ] **Step 4: Implement handleImagePull**

```go
func (dp *DockerProxy) handleImagePull(w http.ResponseWriter, r *http.Request) {
	fromImage := r.URL.Query().Get("fromImage")
	tag := r.URL.Query().Get("tag")

	image := fromImage
	if tag != "" {
		image = fromImage + ":" + tag
	}

	if !dp.policy.ImageAllowed(image) {
		writeError(w, http.StatusForbidden, fmt.Sprintf("image %q not in allowlist", image))
		return
	}

	// Forward to Docker daemon
	dp.upstream.ServeHTTP(w, r)
}
```

- [ ] **Step 5: Implement handleContainerList**

```go
func (dp *DockerProxy) handleContainerList(w http.ResponseWriter, r *http.Request) {
	// Add label filter to only show sandbox-owned containers
	q := r.URL.Query()
	filters := fmt.Sprintf(`{"label":["agent-sandbox.sandbox=%s"]}`, dp.cfg.SandboxID)
	q.Set("filters", filters)
	r.URL.RawQuery = q.Encode()

	dp.upstream.ServeHTTP(w, r)
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./core/plugins/agent-docker/cmd/docker-proxy/ -run "TestDockerProxy_Container|TestDockerProxy_Image" -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add core/plugins/agent-docker/cmd/docker-proxy/proxy.go core/plugins/agent-docker/cmd/docker-proxy/proxy_test.go
git commit -m "feat(agent-docker): container create, image pull, and list handlers"
```

---

### Task 7: Cleanup on Shutdown

**Files:**
- Create: `core/plugins/agent-docker/cmd/docker-proxy/cleanup.go`
- Create: `core/plugins/agent-docker/cmd/docker-proxy/cleanup_test.go`

- [ ] **Step 1: Write test for cleanup logic**

```go
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanup_ListsAndRemovesContainers(t *testing.T) {
	// Track API calls made to the mock Docker daemon
	var calls []string

	mockDocker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)

		switch {
		case r.Method == "GET" && r.URL.Path == "/containers/json":
			// Return two containers matching our sandbox label
			containers := []map[string]any{
				{"Id": "abc123", "Names": []string{"/test-coder-app1"}},
				{"Id": "def456", "Names": []string{"/test-coder-app2"}},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(containers)
		case r.Method == "POST" && (r.URL.Path == "/containers/abc123/stop" || r.URL.Path == "/containers/def456/stop"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "DELETE" && (r.URL.Path == "/containers/abc123" || r.URL.Path == "/containers/def456"):
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockDocker.Close()

	cleaner := &Cleaner{
		sandboxID:  "test",
		dockerAddr: mockDocker.URL,
	}

	cleaner.CleanupAll(context.Background())

	// Should have listed, then stopped and removed both containers
	assert.Contains(t, calls, "GET /containers/json")
	assert.Contains(t, calls, "POST /containers/abc123/stop")
	assert.Contains(t, calls, "POST /containers/def456/stop")
	assert.Contains(t, calls, "DELETE /containers/abc123")
	assert.Contains(t, calls, "DELETE /containers/def456")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/plugins/agent-docker/cmd/docker-proxy/ -run TestCleanup -v`
Expected: FAIL

- [ ] **Step 3: Implement cleanup.go**

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Cleaner handles container cleanup on shutdown.
type Cleaner struct {
	sandboxID  string
	dockerAddr string // "unix:///var/run/docker.sock" or "http://..." for testing
}

// NewCleaner creates a cleaner that talks to the Docker daemon.
func NewCleaner(sandboxID string) *Cleaner {
	return &Cleaner{
		sandboxID:  sandboxID,
		dockerAddr: "unix",
	}
}

func (c *Cleaner) httpClient() *http.Client {
	if c.dockerAddr == "unix" {
		return &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", "/var/run/docker.sock")
				},
			},
			Timeout: 30 * time.Second,
		}
	}
	// For testing with HTTP
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Cleaner) baseURL() string {
	if c.dockerAddr == "unix" {
		return "http://docker"
	}
	return c.dockerAddr
}

// CleanupAll stops and removes all containers labeled with this sandbox ID.
func (c *Cleaner) CleanupAll(ctx context.Context) {
	client := c.httpClient()
	base := c.baseURL()

	// List containers with our sandbox label
	filters := fmt.Sprintf(`{"label":["agent-sandbox.sandbox=%s"]}`, c.sandboxID)
	listURL := fmt.Sprintf("%s/containers/json?all=true&filters=%s", base, filters)

	resp, err := client.Get(listURL)
	if err != nil {
		slog.Error("cleanup: list containers", "error", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var containers []struct {
		Id    string   `json:"Id"`
		Names []string `json:"Names"`
	}
	if err := json.Unmarshal(body, &containers); err != nil {
		slog.Error("cleanup: parse container list", "error", err)
		return
	}

	slog.Info("cleanup: found containers", "count", len(containers))

	for _, container := range containers {
		id := container.Id
		name := ""
		if len(container.Names) > 0 {
			name = container.Names[0]
		}

		// Stop (5s timeout)
		stopURL := fmt.Sprintf("%s/containers/%s/stop?t=5", base, id)
		req, _ := http.NewRequestWithContext(ctx, "POST", stopURL, nil)
		stopResp, err := client.Do(req)
		if err != nil {
			slog.Warn("cleanup: stop container", "id", id[:12], "error", err)
		} else {
			stopResp.Body.Close()
		}

		// Remove
		removeURL := fmt.Sprintf("%s/containers/%s?force=true", base, id)
		req, _ = http.NewRequestWithContext(ctx, "DELETE", removeURL, nil)
		rmResp, err := client.Do(req)
		if err != nil {
			slog.Warn("cleanup: remove container", "id", id[:12], "error", err)
		} else {
			rmResp.Body.Close()
		}

		slog.Info("cleanup: removed container", "id", id[:12], "name", name)
	}
}
```

- [ ] **Step 4: Wire cleanup into DockerProxy.Cleanup()**

In `proxy.go`, update the `Cleanup` method:

```go
func (dp *DockerProxy) Cleanup() {
	cleaner := NewCleaner(dp.cfg.SandboxID)
	cleaner.CleanupAll(context.Background())
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./core/plugins/agent-docker/cmd/docker-proxy/ -run TestCleanup -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add core/plugins/agent-docker/cmd/docker-proxy/cleanup.go core/plugins/agent-docker/cmd/docker-proxy/cleanup_test.go core/plugins/agent-docker/cmd/docker-proxy/proxy.go
git commit -m "feat(agent-docker): cleanup handler — stop and remove containers on shutdown"
```

---

### Task 8: Generator — Inject System Env Vars into Sidecars

**Files:**
- Modify: `internal/generate/v1/compose.go`
- Modify: `internal/generate/v1/compose_test.go`

- [ ] **Step 1: Write failing test for system-injected sidecar env vars**

```go
func TestBuildProjectCompose_SidecarSystemEnvVars(t *testing.T) {
	agents := []ComposeAgentEntry{{
		Config: &config.Config{
			Name: "coder",
			Runtime: config.RuntimeConfig{
				CWD: "/home/agent/workspace",
			},
		},
		Contribs: &plugin.Contributions{
			Sidecar: plugin.SidecarContrib{
				Services: map[string]plugin.ComposeService{
					"my-sidecar": {
						Image: "alpine:3.20",
					},
				},
			},
		},
		BuildDir: t.TempDir(),
	}}

	output, err := BuildProjectCompose(agents, t.TempDir())
	require.NoError(t, err)

	var compose struct {
		Services map[string]any `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(output), &compose))

	sidecar, ok := compose.Services["coder-my-sidecar"].(map[string]any)
	require.True(t, ok)

	env, ok := sidecar["environment"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "coder", env["AGENT_NAME"])
	assert.Contains(t, env["SANDBOX_ID"], "coder")
	assert.Contains(t, env["SANDBOX_NETWORK"], "_sandbox")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/generate/v1/ -run TestBuildProjectCompose_SidecarSystemEnvVars -v`
Expected: FAIL

- [ ] **Step 3: Update `buildSidecarService` to inject system env vars**

In `internal/generate/v1/compose.go`, update `buildAgentPair` where sidecar services are built (around line 169). After `buildSidecarService` is called, inject system env vars:

```go
// Sidecar services from plugins
if contribs != nil {
    for name, svc := range contribs.Sidecar.Services {
        sidecar := buildSidecarService(svc, p.buildDir)
        // Inject system env vars into all sidecars
        injectSidecarSystemEnv(sidecar, p.cfg.Name, projectName)
        // Sidecars implicitly depend on the agent service being started.
        if sidecar["depends_on"] == nil {
            sidecar["depends_on"] = map[string]any{
                p.agentName: map[string]any{
                    "condition": "service_healthy",
                },
            }
        }
        sidecarName := name
        if p.sidecarPrefix != "" {
            sidecarName = p.sidecarPrefix + "-" + name
        }
        result.services[sidecarName] = sidecar
    }
}
```

Add the helper:

```go
// injectSidecarSystemEnv adds well-known env vars to a sidecar service.
// These provide the sidecar with sandbox identity and network information.
func injectSidecarSystemEnv(sidecar map[string]any, agentName, projectName string) {
    env, ok := sidecar["environment"].(map[string]string)
    if !ok {
        env = make(map[string]string)
    }
    env["SANDBOX_ID"] = projectName + "-" + agentName
    env["SANDBOX_NETWORK"] = projectName + "_sandbox"
    env["AGENT_NAME"] = agentName
    sidecar["environment"] = env
}
```

Note: `projectName` needs to be threaded through to `buildAgentPair`. Add it to `agentPairParams`:

```go
type agentPairParams struct {
    // ... existing fields ...
    projectName string // compose project name (folder basename)
}
```

And in `BuildProjectCompose`, derive it:

```go
projectName := filepath.Base(projectDir)
```

Pass it through to `buildAgentPair` via the params struct.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/generate/v1/ -run TestBuildProjectCompose_SidecarSystemEnvVars -v`
Expected: PASS

- [ ] **Step 5: Run all compose tests**

Run: `go test ./internal/generate/v1/ -v`
Expected: All pass

- [ ] **Step 6: Commit**

```bash
git add internal/generate/v1/compose.go internal/generate/v1/compose_test.go
git commit -m "feat: generator injects SANDBOX_ID, SANDBOX_NETWORK, AGENT_NAME into sidecars"
```

---

### Task 9: Plugin YAML Definition

**Files:**
- Create: `core/plugins/agent-docker/plugin.yaml`

- [ ] **Step 1: Create the plugin.yaml**

```yaml
name: agent-docker
assets:
  - path: cmd/docker-proxy/
    exclude: [*_test.go]

options:
  allowed_images:
    type: array
    items:
      type: string
    required: true
    description: "Glob patterns for allowed Docker images (e.g. node:20-*, postgres:16-*)"
  max_containers:
    type: number
    required: false
    default: 5
    description: "Maximum concurrent containers the agent can spawn"
  memory:
    type: string
    required: false
    default: "2g"
    description: "Memory limit per spawned container (e.g. 2g, 512m)"
  cpus:
    type: string
    required: false
    default: "2"
    description: "CPU limit per spawned container (e.g. 2, 0.5)"
  pids:
    type: number
    required: false
    default: 256
    description: "PID limit per spawned container"

contributes:
  runtime:
    environment:
      DOCKER_HOST: "tcp://{{ .agent.name }}-docker-proxy:2375"
  sidecar:
    services:
      {{ .agent.name }}-docker-proxy:
        build: "{{ asset \"cmd/docker-proxy\" }}"
        volumes:
          - "/var/run/docker.sock:/var/run/docker.sock"
        environment:
          ALLOWED_IMAGES: '{{ toJSON .plugin.options.allowed_images }}'
          MAX_CONTAINERS: "{{ .plugin.options.max_containers }}"
          MEMORY_LIMIT: "{{ .plugin.options.memory }}"
          CPU_LIMIT: "{{ .plugin.options.cpus }}"
          PID_LIMIT: "{{ .plugin.options.pids }}"
```

Note: `SANDBOX_ID`, `SANDBOX_NETWORK`, and `AGENT_NAME` are not declared here — the generator injects them into all sidecars automatically. `networks` is also not declared — the generator assigns all sidecars to the sandbox network.

- [ ] **Step 2: Verify plugin resolves correctly**

Run: `go test ./internal/plugin/ -run TestResolve -v` (after adding fixture)

Or verify manually:
```bash
go run ./cmd/agent-sandbox-core/ generate -C examples/local-coding 2>&1 | head -20
```
(This will fail if plugin.yaml has syntax issues)

- [ ] **Step 3: Commit**

```bash
git add core/plugins/agent-docker/plugin.yaml
git commit -m "feat(agent-docker): plugin.yaml — sidecar definition and options"
```

---

### Task 10: Build Verification and Full Test Run

- [ ] **Step 1: Verify the proxy binary builds**

Run: `go build ./core/plugins/agent-docker/cmd/docker-proxy/`
Expected: Clean build

- [ ] **Step 2: Run all proxy tests**

Run: `go test ./core/plugins/agent-docker/... -v`
Expected: All pass

- [ ] **Step 3: Run full project build**

Run: `go build ./...`
Expected: Clean build

- [ ] **Step 4: Run linter**

Run: `golangci-lint run ./...`
Expected: No new lint issues

- [ ] **Step 5: Run full project tests**

Run: `go test ./...`
Expected: All pass

- [ ] **Step 6: Final commit if any fixups needed**

```bash
git commit -m "chore: lint and build fixes for agent-docker plugin"
```
