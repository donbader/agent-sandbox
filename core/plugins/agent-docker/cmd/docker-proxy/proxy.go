package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
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
	volumes  *VolumeTranslator
	names    *NameTranslator    // bidirectional name mapping for containers, networks, volumes
	mu       sync.Mutex
	ids         map[string]bool // container IDs owned by this sandbox (for counting)
	tracked     map[string]bool // all lookup keys (IDs + namespaced names) for ownership checks
	networks    map[string]bool // network IDs created by this sandbox (for cleanup)
	builtImages map[string]bool // image tags built through this proxy (auto-allowed)
}

// dialUpstream returns a net.Conn to the upstream Docker daemon.
// Respects DOCKER_HOST env var (standard Docker convention).
func dialUpstream() (net.Conn, error) {
	if dh := os.Getenv("DOCKER_HOST"); strings.HasPrefix(dh, "tcp://") {
		return net.Dial("tcp", strings.TrimPrefix(dh, "tcp://"))
	}
	return net.Dial("unix", "/var/run/docker.sock")
}

// NewDockerProxy creates a new Docker API proxy.
func NewDockerProxy(cfg *ProxyConfig) (*DockerProxy, error) {
	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return dialUpstream()
		},
	}
	upstream := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = "docker"
		},
		Transport: transport,
	}

	builtImages := make(map[string]bool)

	return &DockerProxy{
		policy: &Policy{
			AllowedImages:       cfg.AllowedImages,
			MaxContainers:       cfg.MaxContainers,
			AllowBuild:          cfg.AllowBuild,
			BuiltImages:         builtImages,
			AllowedCapabilities: cfg.AllowedCapabilities,
		},
		mutator:     NewMutator(cfg),
		cfg:         cfg,
		upstream:    upstream,
		names:       NewNameTranslator(cfg.SandboxID),
		ids:         make(map[string]bool),
		tracked:     make(map[string]bool),
		networks:    make(map[string]bool),
		builtImages: builtImages,
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

	if !dp.isEndpointAllowed(r.Method, path) {
		writeError(w, http.StatusForbidden, "endpoint not allowed")
		return
	}

	// Route to specific handlers
	switch {
	case r.Method == "POST" && path == "/containers/create":
		dp.handleContainerCreate(w, r)
	case r.Method == "POST" && path == "/images/create":
		dp.handleImagePull(w, r)
	case r.Method == "POST" && path == "/build":
		dp.handleBuild(w, r)
	case r.Method == "POST" && strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/tag"):
		dp.handleImageTag(w, r, path)
	case r.Method == "GET" && path == "/containers/json":
		dp.handleContainerList(w, r)
	case r.Method == "POST" && path == "/networks/create":
		dp.handleNetworkCreate(w, r)
	case r.Method == "DELETE" && strings.HasPrefix(path, "/networks/"):
		dp.handleNetworkRemove(w, r, path)
	case r.Method == "GET" && path == "/networks":
		dp.handleNetworkList(w, r)
	case r.Method == "POST" && strings.HasPrefix(path, "/networks/") && strings.HasSuffix(path, "/connect"):
		dp.handleNetworkConnect(w, r)
	case r.Method == "POST" && path == "/volumes/create":
		dp.handleVolumeCreate(w, r)
	case r.Method == "GET" && path == "/volumes":
		dp.handleVolumeList(w, r)
	case r.Method == "GET" && strings.HasPrefix(path, "/volumes/"):
		dp.handleVolumeInspect(w, r, path)
	case r.Method == "DELETE" && strings.HasPrefix(path, "/volumes/"):
		dp.handleVolumeRemove(w, r, path)
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
		// Handle hijack/upgrade requests (attach, exec start)
		if isHijackEndpoint(path) {
			dp.handleHijack(w, r)
			return
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
	defer func() { _ = r.Body.Close() }()

	var body map[string]any
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Translate host bind mounts to volume-subpath mounts (DooD support)
	if dp.volumes != nil {
		if err := dp.translateBindMounts(body); err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
	}

	createReq := extractCreateRequest(body)

	// Validate under lock to prevent TOCTOU race on container count
	dp.mu.Lock()
	currentCount := len(dp.ids)
	if err := dp.policy.ValidateCreate(createReq, currentCount); err != nil {
		dp.mu.Unlock()
		if pe, ok := err.(*PolicyError); ok {
			writeError(w, pe.Code, pe.Message)
		} else {
			writeError(w, http.StatusForbidden, err.Error())
		}
		return
	}
	dp.mu.Unlock()

	containerName := r.URL.Query().Get("name")
	namespacedName := dp.mutator.NamespaceContainerName(containerName)

	// If allow_compose, resolve image defaults so init wrapper can preserve them
	if dp.cfg.AllowCompose {
		dp.resolveImageDefaults(body)
	}

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

	rec := &responseRecorder{header: make(http.Header)}
	dp.upstream.ServeHTTP(rec, newReq)

	if rec.code == http.StatusCreated {
		var resp map[string]any
		if err := json.Unmarshal(rec.body.Bytes(), &resp); err == nil {
			if id, ok := resp["Id"].(string); ok {
				dp.trackContainer(id, containerName, namespacedName)
				slog.Info("container created", "id", id[:min(12, len(id))], "name", namespacedName, "image", createReq.Image)
			}
		}
	}

	for k, v := range rec.header {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.code)
	_, _ = w.Write(rec.body.Bytes())
}

// resolveImageDefaults inspects the image and populates Entrypoint/Cmd in the
// body if they are not explicitly set. This ensures the init wrapper can
// preserve the image's default command when overriding the entrypoint.
func (dp *DockerProxy) resolveImageDefaults(body map[string]any) {
	image, _ := body["Image"].(string)
	if image == "" {
		return
	}

	// Only resolve if Entrypoint AND Cmd are both present and non-nil
	ep, _ := body["Entrypoint"].([]any)
	cmd, _ := body["Cmd"].([]any)
	if len(ep) > 0 && len(cmd) > 0 {
		return
	}

	req, err := http.NewRequest("GET", fmt.Sprintf("/images/%s/json", image), nil)
	if err != nil {
		slog.Debug("resolveImageDefaults: failed to create request", "error", err)
		return
	}
	req.URL.Scheme = "http"
	req.URL.Host = "docker"

	rec := &responseRecorder{header: make(http.Header)}
	dp.upstream.ServeHTTP(rec, req)

	if rec.code != http.StatusOK {
		slog.Debug("resolveImageDefaults: image inspect failed", "image", image, "code", rec.code)
		return
	}

	var imgInfo struct {
		Config struct {
			Entrypoint []string `json:"Entrypoint"`
			Cmd        []string `json:"Cmd"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(rec.body.Bytes(), &imgInfo); err != nil {
		slog.Debug("resolveImageDefaults: failed to parse image info", "error", err)
		return
	}

	if len(ep) == 0 && len(imgInfo.Config.Entrypoint) > 0 {
		imgEP := make([]any, len(imgInfo.Config.Entrypoint))
		for i, s := range imgInfo.Config.Entrypoint {
			imgEP[i] = s
		}
		body["Entrypoint"] = imgEP
	}
	if len(cmd) == 0 && len(imgInfo.Config.Cmd) > 0 {
		imgCmd := make([]any, len(imgInfo.Config.Cmd))
		for i, s := range imgInfo.Config.Cmd {
			imgCmd[i] = s
		}
		body["Cmd"] = imgCmd
	}
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

// handleBuild forwards a POST /build request and tracks the resulting image tags.
func (dp *DockerProxy) handleBuild(w http.ResponseWriter, r *http.Request) {
	tags := r.URL.Query()["t"]

	dp.upstream.ServeHTTP(w, r)

	if len(tags) > 0 && dp.cfg.AllowBuild {
		dp.mu.Lock()
		for _, tag := range tags {
			if tag != "" {
				dp.builtImages[tag] = true
				dp.builtImages[normalizeImage(tag)] = true
				slog.Info("tracked built image", "tag", tag)
			}
		}
		dp.mu.Unlock()
	}
}

// handleImageTag forwards a POST /images/{name}/tag request and tracks the resulting tag.
func (dp *DockerProxy) handleImageTag(w http.ResponseWriter, r *http.Request, path string) {
	repo := r.URL.Query().Get("repo")
	tag := r.URL.Query().Get("tag")

	dp.upstream.ServeHTTP(w, r)

	if dp.cfg.AllowBuild && repo != "" {
		result := repo
		if tag != "" {
			result = repo + ":" + tag
		}
		dp.mu.Lock()
		dp.builtImages[result] = true
		dp.builtImages[normalizeImage(result)] = true
		slog.Info("tracked tagged image", "image", result)
		dp.mu.Unlock()
	}
}

func (dp *DockerProxy) handleContainerList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filters := fmt.Sprintf(`{"label":["agent-sandbox.sandbox=%s"]}`, dp.cfg.SandboxID)
	q.Set("filters", filters)
	r.URL.RawQuery = q.Encode()

	rec := &responseRecorder{header: make(http.Header)}
	dp.upstream.ServeHTTP(rec, r)

	body := dp.names.TranslateNames(KindContainer, rec.body.Bytes())

	for k, v := range rec.header {
		w.Header()[k] = v
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(rec.code)
	_, _ = w.Write(body)
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

// responseRecorder captures an HTTP response for inspection before forwarding.
type responseRecorder struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func (r *responseRecorder) Header() http.Header { return r.header }
func (r *responseRecorder) WriteHeader(code int) { r.code = code }
func (r *responseRecorder) Write(b []byte) (int, error) {
	return r.body.Write(b)
}

// isEndpointAllowed checks if a method+path combination is in the allowlist.
func (dp *DockerProxy) isEndpointAllowed(method, path string) bool {
	for _, rule := range allowedEndpoints {
		if rule.method == method && rule.pattern.MatchString(path) {
			return true
		}
	}
	if dp.cfg.AllowCompose {
		for _, rule := range composeEndpoints {
			if rule.method == method && rule.pattern.MatchString(path) {
				return true
			}
		}
	}
	if dp.cfg.AllowBuild {
		for _, rule := range buildEndpoints {
			if rule.method == method && rule.pattern.MatchString(path) {
				return true
			}
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
	{"GET", regexp.MustCompile(`^/images/[a-zA-Z0-9_./:@-]+/json$`)},
	{"POST", regexp.MustCompile(`^/images/create$`)},
	// Distribution (used by docker pull for manifest checks)
	{"GET", regexp.MustCompile(`^/distribution/`)},
}

// buildEndpoints are only allowed when AllowBuild is true.
var buildEndpoints = []endpointRule{
	{"GET", regexp.MustCompile(`^/info$`)},
	{"POST", regexp.MustCompile(`^/build$`)},
	{"GET", regexp.MustCompile(`^/images/.+/get$`)},
	{"POST", regexp.MustCompile(`^/images/load$`)},
	// Tagging images (needed after docker build to assign name:tag)
	{"POST", regexp.MustCompile(`^/images/.+/tag$`)},
	// Deleting images (cleanup after build)
	{"DELETE", regexp.MustCompile(`^/images/.+$`)},
	// Build history/cache inspection
	{"GET", regexp.MustCompile(`^/images/.+/history$`)},
}

// composeEndpoints are only allowed when AllowCompose is true.
var composeEndpoints = []endpointRule{
	{"POST", regexp.MustCompile(`^/networks/create$`)},
	{"DELETE", regexp.MustCompile(`^/networks/[a-zA-Z0-9_.-]+$`)},
	{"GET", regexp.MustCompile(`^/networks$`)},
	{"GET", regexp.MustCompile(`^/networks/[a-zA-Z0-9_.-]+$`)},
	{"POST", regexp.MustCompile(`^/networks/[a-zA-Z0-9_.-]+/connect$`)},
	{"POST", regexp.MustCompile(`^/networks/[a-zA-Z0-9_.-]+/disconnect$`)},
	// Volumes (compose needs them for named volumes between inner services)
	{"POST", regexp.MustCompile(`^/volumes/create$`)},
	{"GET", regexp.MustCompile(`^/volumes$`)},
	{"GET", regexp.MustCompile(`^/volumes/[a-zA-Z0-9_.-]+$`)},
	{"DELETE", regexp.MustCompile(`^/volumes/[a-zA-Z0-9_.-]+$`)},
}

// extractContainerID pulls the container ID from paths like /containers/{id}/start.
func extractContainerID(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "containers" && parts[1] != "json" && parts[1] != "create" {
		return parts[1]
	}
	return ""
}

// resolveContainerRef translates a user-provided container reference to the actual
// namespaced name/ID. Returns empty string if not owned.
func (dp *DockerProxy) resolveContainerRef(ref string) string {
	// Try name translation first (user name → namespaced name, or real name passthrough)
	if resolved := dp.names.Resolve(KindContainer, ref); resolved != "" {
		return resolved
	}
	dp.mu.Lock()
	defer dp.mu.Unlock()
	// Direct match (full ID or namespaced name)
	if dp.tracked[ref] {
		return ref
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
	dp.ids[id] = true
	dp.tracked[id] = true
	dp.tracked[namespacedName] = true
	dp.names.Track(KindContainer, userName, namespacedName)
}

// isHijackEndpoint returns true for endpoints that require HTTP connection upgrade.
func isHijackEndpoint(path string) bool {
	// /containers/{id}/attach and /exec/{id}/start use connection hijacking
	if strings.Contains(path, "/attach") {
		return true
	}
	if strings.HasPrefix(path, "/exec/") && strings.HasSuffix(path, "/start") {
		return true
	}
	return false
}

// handleHijack handles Docker API endpoints that upgrade the HTTP connection
// to a raw bidirectional TCP stream (attach, exec start).
func (dp *DockerProxy) handleHijack(w http.ResponseWriter, r *http.Request) {
	slog.Debug("hijack request", "method", r.Method, "path", r.URL.Path, "upgrade", r.Header.Get("Upgrade"))

	// Connect to Docker daemon
	dockerConn, err := dialUpstream()
	if err != nil {
		writeError(w, http.StatusBadGateway, "cannot connect to Docker daemon")
		return
	}
	defer func() { _ = dockerConn.Close() }()

	// Write the original HTTP request to Docker daemon
	if err := r.Write(dockerConn); err != nil {
		writeError(w, http.StatusBadGateway, "failed to forward request to Docker daemon")
		return
	}

	// Hijack the client connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, http.StatusInternalServerError, "hijack not supported")
		return
	}

	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		slog.Error("hijack failed", "error", err)
		return
	}
	defer func() { _ = clientConn.Close() }()

	slog.Debug("hijack established", "path", r.URL.Path)

	// Bidirectional pipe
	var wg sync.WaitGroup
	wg.Add(2)

	// Docker daemon → client
	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, dockerConn)
		if tc, ok := clientConn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()

	// Client → Docker daemon (flush any buffered data first)
	go func() {
		defer wg.Done()
		if clientBuf.Reader.Buffered() > 0 {
			buffered := make([]byte, clientBuf.Reader.Buffered())
			_, _ = clientBuf.Read(buffered)
			_, _ = dockerConn.Write(buffered)
		}
		_, _ = io.Copy(dockerConn, clientConn)
		if cw, ok := dockerConn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()

	wg.Wait()
	slog.Debug("hijack completed", "path", r.URL.Path)
}
