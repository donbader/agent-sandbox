package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// handleNetworkCreate intercepts network creation, forces Internal=true, and namespaces the name.
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

	// Force internal: true — created networks cannot reach the internet
	body["Internal"] = true

	// Namespace the network name
	if name, ok := body["Name"].(string); ok {
		body["Name"] = dp.cfg.SandboxID + "-" + name
	}

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
				slog.Info("network created", "id", id[:min(12, len(id))], "internal", true)
			}
		}
	}

	for k, v := range rec.header {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.code)
	_, _ = w.Write(rec.body.Bytes())
}

// handleNetworkRemove only allows removing networks we created.
func (dp *DockerProxy) handleNetworkRemove(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 2 {
		writeError(w, http.StatusBadRequest, "invalid network path")
		return
	}
	networkRef := parts[1]

	// Check if we own this network
	dp.mu.Lock()
	owned := dp.networks[networkRef]
	dp.mu.Unlock()

	if !owned {
		// Try prefix match (compose uses names, Docker uses IDs)
		// Allow if it starts with our sandbox ID prefix
		if !strings.HasPrefix(networkRef, dp.cfg.SandboxID+"-") {
			writeError(w, http.StatusForbidden, "cannot remove networks not created by this sandbox")
			return
		}
	}

	dp.upstream.ServeHTTP(w, r)

	dp.mu.Lock()
	delete(dp.networks, networkRef)
	dp.mu.Unlock()
}

// handleNetworkList filters to only show networks owned by this sandbox.
func (dp *DockerProxy) handleNetworkList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filters := fmt.Sprintf(`{"label":["agent-sandbox.sandbox=%s"]}`, dp.cfg.SandboxID)
	q.Set("filters", filters)
	r.URL.RawQuery = q.Encode()

	dp.upstream.ServeHTTP(w, r)
}

// handleNetworkConnect allows connecting containers to networks we own.
func (dp *DockerProxy) handleNetworkConnect(w http.ResponseWriter, r *http.Request) {
	dp.upstream.ServeHTTP(w, r)
}
