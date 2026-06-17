package main

import (
	"context"
	"encoding/json"
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
	// Strip Docker API version prefix (e.g., /v1.43/containers/json -> /containers/json)
	path := r.URL.Path
	if strings.HasPrefix(path, "/v") {
		if idx := strings.Index(path[1:], "/"); idx > 0 {
			path = path[idx+1:]
		}
	}

	if !isEndpointAllowed(r.Method, path) {
		writeError(w, http.StatusForbidden, "endpoint not allowed")
		return
	}

	// Route to specific handlers
	switch {
	case r.Method == "POST" && path == "/containers/create":
		dp.handleContainerCreate(w, r)
	case r.Method == "POST" && path == "/images/create":
		dp.handleImagePull(w, r)
	case r.Method == "GET" && path == "/containers/json":
		dp.handleContainerList(w, r)
	default:
		// For namespace-checked endpoints, verify ownership
		if id := extractContainerID(path); id != "" {
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
	// Will be implemented in cleanup.go
}

// handleContainerCreate — stub, implemented in next task.
func (dp *DockerProxy) handleContainerCreate(w http.ResponseWriter, r *http.Request) {
	dp.upstream.ServeHTTP(w, r)
}

// handleImagePull — stub, implemented in next task.
func (dp *DockerProxy) handleImagePull(w http.ResponseWriter, r *http.Request) {
	dp.upstream.ServeHTTP(w, r)
}

// handleContainerList — stub, implemented in next task.
func (dp *DockerProxy) handleContainerList(w http.ResponseWriter, r *http.Request) {
	dp.upstream.ServeHTTP(w, r)
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

// extractContainerID pulls the container ID from paths like /containers/{id}/start.
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
