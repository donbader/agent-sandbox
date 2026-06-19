package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// handleNetworkCreate intercepts network creation, namespaces the name, and forces Internal=true.
func (dp *DockerProxy) handleNetworkCreate(w http.ResponseWriter, r *http.Request) {
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

	// Namespace the network name
	userName, _ := body["Name"].(string)
	namespacedName := dp.names.Namespace(KindNetwork, userName)
	body["Name"] = namespacedName

	// Force internal: true — created networks cannot reach the internet
	body["Internal"] = true

	// Add sandbox label for tracking
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

	if rec.code == http.StatusCreated {
		var resp map[string]any
		if err := json.Unmarshal(rec.body.Bytes(), &resp); err == nil {
			if id, ok := resp["Id"].(string); ok {
				dp.mu.Lock()
				dp.networks[id] = true
				dp.mu.Unlock()
				dp.names.Track(KindNetwork, userName, namespacedName)
				slog.Info("network created", "id", id[:min(12, len(id))], "name", namespacedName, "internal", true)
			}
		}
	}

	respBody := dp.names.TranslateNames(KindNetwork, rec.body.Bytes())
	for k, v := range rec.header {
		w.Header()[k] = v
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(respBody)))
	w.WriteHeader(rec.code)
	_, _ = w.Write(respBody)
}

// handleNetworkRemove only allows removing networks we created.
func (dp *DockerProxy) handleNetworkRemove(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 2 {
		writeError(w, http.StatusBadRequest, "invalid network path")
		return
	}
	networkRef := parts[1]

	// Resolve user-visible name to real (namespaced) name if known
	if resolved := dp.names.Resolve(KindNetwork, networkRef); resolved != "" && resolved != networkRef {
		networkRef = resolved
		r.URL.Path = strings.Replace(r.URL.Path, parts[1], networkRef, 1)
	}

	// Check if we own this network (by ID)
	dp.mu.Lock()
	owned := dp.networks[networkRef]
	dp.mu.Unlock()

	if !owned {
		// Allow removal by name for compose workflows — compose creates
		// and removes its own project-scoped networks by name.
		// Verify via label that this network belongs to our sandbox.
		if !dp.networkOwnedByLabel(networkRef) {
			writeError(w, http.StatusForbidden, "cannot remove networks not created by this sandbox")
			return
		}
	}

	dp.upstream.ServeHTTP(w, r)

	dp.mu.Lock()
	delete(dp.networks, networkRef)
	dp.mu.Unlock()
	dp.names.Untrack(KindNetwork, networkRef)
}

// handleNetworkList filters to only show networks owned by this sandbox.
func (dp *DockerProxy) handleNetworkList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filters := fmt.Sprintf(`{"label":["agent-sandbox.sandbox=%s"]}`, dp.cfg.SandboxID)
	q.Set("filters", filters)
	r.URL.RawQuery = q.Encode()

	rec := &responseRecorder{header: make(http.Header)}
	dp.upstream.ServeHTTP(rec, r)

	body := dp.names.TranslateNames(KindNetwork, rec.body.Bytes())

	for k, v := range rec.header {
		w.Header()[k] = v
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(rec.code)
	_, _ = w.Write(body)
}

// handleNetworkConnect allows connecting containers to networks we own.
func (dp *DockerProxy) handleNetworkConnect(w http.ResponseWriter, r *http.Request) {
	dp.upstream.ServeHTTP(w, r)
}

// networkOwnedByLabel inspects a network and checks if it has our sandbox label.
func (dp *DockerProxy) networkOwnedByLabel(networkRef string) bool {
	req, err := http.NewRequest("GET", fmt.Sprintf("/networks/%s", networkRef), nil)
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
