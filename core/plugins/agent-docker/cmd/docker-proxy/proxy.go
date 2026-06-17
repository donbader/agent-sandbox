package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
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
	tracked  map[string]bool   // container IDs/namespaced names owned by this sandbox
	nameMap  map[string]string // user-provided name → namespaced name
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
		nameMap:  make(map[string]string),
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
		// For namespace-checked endpoints, verify ownership and translate names
		if id := extractContainerID(path); id != "" {
			resolvedID := dp.resolveContainerRef(id)
			if resolvedID == "" {
				writeError(w, http.StatusNotFound, "container not found")
				return
			}
			// Rewrite path with resolved ID/name
			if resolvedID != id {
				r.URL.Path = strings.Replace(r.URL.Path, id, resolvedID, 1)
			}
		}
		dp.upstream.ServeHTTP(w, r)
	}
}

// Cleanup stops and removes all tracked containers.
func (dp *DockerProxy) Cleanup() {
	cleaner := NewCleaner(dp.cfg.SandboxID)
	cleaner.CleanupAll(context.Background())
}

func (dp *DockerProxy) handleContainerCreate(w http.ResponseWriter, r *http.Request) {
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

	createReq := extractCreateRequest(body)

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

	containerName := r.URL.Query().Get("name")
	namespacedName := dp.mutator.NamespaceContainerName(containerName)

	dp.mutator.MutateCreate(body, namespacedName)

	mutatedBody, _ := json.Marshal(body)

	newURL := *r.URL
	q := newURL.Query()
	q.Set("name", namespacedName)
	newURL.RawQuery = q.Encode()

	newReq, _ := http.NewRequestWithContext(r.Context(), r.Method, newURL.String(), strings.NewReader(string(mutatedBody)))
	newReq.Header = r.Header.Clone()
	newReq.Header.Set("Content-Length", fmt.Sprintf("%d", len(mutatedBody)))
	newReq.ContentLength = int64(len(mutatedBody))

	rec := httptest.NewRecorder()
	dp.upstream.ServeHTTP(rec, newReq)

	if rec.Code == http.StatusCreated {
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
			if id, ok := resp["Id"].(string); ok {
				dp.trackContainer(id, containerName, namespacedName)
				slog.Info("container created", "id", id[:min(12, len(id))], "name", namespacedName, "image", createReq.Image)
			}
		}
	}

	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

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

	dp.upstream.ServeHTTP(w, r)
}

func (dp *DockerProxy) handleContainerList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filters := fmt.Sprintf(`{"label":["agent-sandbox.sandbox=%s"]}`, dp.cfg.SandboxID)
	q.Set("filters", filters)
	r.URL.RawQuery = q.Encode()

	dp.upstream.ServeHTTP(w, r)
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
	// Docker CLI essentials
	{"GET", regexp.MustCompile(`^/_ping$`)},
	{"HEAD", regexp.MustCompile(`^/_ping$`)},
	{"GET", regexp.MustCompile(`^/version$`)},
	// Container lifecycle
	{"POST", regexp.MustCompile(`^/containers/create$`)},
	{"POST", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+/start$`)},
	{"POST", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+/stop$`)},
	{"POST", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+/kill$`)},
	{"POST", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+/wait$`)},
	{"POST", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+/resize$`)},
	{"POST", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+/attach$`)},
	{"DELETE", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+$`)},
	{"GET", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+/json$`)},
	{"GET", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+/logs$`)},
	{"GET", regexp.MustCompile(`^/containers/json$`)},
	// Exec
	{"POST", regexp.MustCompile(`^/containers/[a-zA-Z0-9_.-]+/exec$`)},
	{"POST", regexp.MustCompile(`^/exec/[a-zA-Z0-9_.-]+/start$`)},
	{"GET", regexp.MustCompile(`^/exec/[a-zA-Z0-9_.-]+/json$`)},
	// Images
	{"GET", regexp.MustCompile(`^/images/json$`)},
	{"GET", regexp.MustCompile(`^/images/[a-zA-Z0-9_./-]+/json$`)},
	{"POST", regexp.MustCompile(`^/images/create$`)},
	// Distribution (used by docker pull for manifest checks)
	{"GET", regexp.MustCompile(`^/distribution/`)},
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

// resolveContainerRef translates a user-provided container reference to the actual
// namespaced name/ID. Returns empty string if not owned.
func (dp *DockerProxy) resolveContainerRef(ref string) string {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	// Direct match (full ID or namespaced name)
	if dp.tracked[ref] {
		return ref
	}
	// Try as user-provided name → namespaced name
	if namespaced, ok := dp.nameMap[ref]; ok {
		return namespaced
	}
	// Try prefix match on container IDs (docker allows short IDs)
	for id := range dp.tracked {
		if len(ref) >= 12 && strings.HasPrefix(id, ref) {
			return id
		}
	}
	return ""
}

func (dp *DockerProxy) trackContainer(id, userName, namespacedName string) {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	dp.tracked[id] = true
	dp.tracked[namespacedName] = true
	if userName != "" {
		dp.nameMap[userName] = namespacedName
	}
}

func (dp *DockerProxy) untrackContainer(id string) {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	delete(dp.tracked, id)
}
