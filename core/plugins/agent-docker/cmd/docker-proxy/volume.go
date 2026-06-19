package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// handleVolumeCreate intercepts volume creation and injects the sandbox label.
func (dp *DockerProxy) handleVolumeCreate(w http.ResponseWriter, r *http.Request) {
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

	// Add sandbox label for ownership tracking
	labels, ok := body["Labels"].(map[string]any)
	if !ok || labels == nil {
		labels = map[string]any{}
	}
	labels["agent-sandbox.sandbox"] = dp.cfg.SandboxID
	body["Labels"] = labels

	mutatedBody, _ := json.Marshal(body)
	newReq, _ := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), strings.NewReader(string(mutatedBody)))
	newReq.Header = r.Header.Clone()
	newReq.Header.Set("Content-Length", fmt.Sprintf("%d", len(mutatedBody)))
	newReq.ContentLength = int64(len(mutatedBody))

	rec := &responseRecorder{header: make(http.Header)}
	dp.upstream.ServeHTTP(rec, newReq)

	if rec.code == http.StatusCreated || rec.code == http.StatusOK {
		name, _ := body["Name"].(string)
		if name != "" {
			slog.Info("volume created", "name", name)
		}
	}

	for k, v := range rec.header {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.code)
	_, _ = w.Write(rec.body.Bytes())
}

// handleVolumeList filters to only show volumes owned by this sandbox.
func (dp *DockerProxy) handleVolumeList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filters := fmt.Sprintf(`{"label":["agent-sandbox.sandbox=%s"]}`, dp.cfg.SandboxID)
	q.Set("filters", filters)
	r.URL.RawQuery = q.Encode()

	dp.upstream.ServeHTTP(w, r)
}

// handleVolumeRemove only allows removing volumes owned by this sandbox.
func (dp *DockerProxy) handleVolumeRemove(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 2 {
		writeError(w, http.StatusBadRequest, "invalid volume path")
		return
	}
	volumeRef := parts[1]

	if !dp.volumeOwnedByLabel(volumeRef) {
		writeError(w, http.StatusForbidden, "cannot remove volumes not created by this sandbox")
		return
	}

	dp.upstream.ServeHTTP(w, r)
}

// handleVolumeInspect only allows inspecting volumes owned by this sandbox.
func (dp *DockerProxy) handleVolumeInspect(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 2 {
		writeError(w, http.StatusBadRequest, "invalid volume path")
		return
	}
	volumeRef := parts[1]

	if !dp.volumeOwnedByLabel(volumeRef) {
		writeError(w, http.StatusNotFound, "volume not found")
		return
	}

	dp.upstream.ServeHTTP(w, r)
}

// volumeOwnedByLabel inspects a volume and checks if it has our sandbox label.
func (dp *DockerProxy) volumeOwnedByLabel(volumeRef string) bool {
	req, err := http.NewRequest("GET", fmt.Sprintf("/volumes/%s", volumeRef), nil)
	if err != nil {
		return false
	}
	req.URL.Scheme = "http"
	req.URL.Host = "docker"

	rec := &responseRecorder{header: make(http.Header)}
	dp.upstream.ServeHTTP(rec, req)
	if rec.code != http.StatusOK {
		return false
	}

	var info struct {
		Labels map[string]string `json:"Labels"`
	}
	if err := json.Unmarshal(rec.body.Bytes(), &info); err != nil {
		return false
	}
	return info.Labels["agent-sandbox.sandbox"] == dp.cfg.SandboxID
}
