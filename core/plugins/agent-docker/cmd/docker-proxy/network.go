package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

// DiscoverSandboxNetwork finds the sandbox network ID by inspecting the proxy's own
// container. This is the correct approach because the compose infrastructure creates
// and manages the network — the proxy just needs to discover which network instance
// the agent stack is actually on, then attach spawned containers to the same one.
//
// Previous design (EnsureSandboxNetwork) was broken: it would create a NEW network
// with the same name if the original was missing, but the agent container would still
// be on the old network instance, making spawned containers unreachable.
func (dp *DockerProxy) DiscoverSandboxNetwork() error {
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("get hostname: %w", err)
	}

	// Inspect our own container to find which networks we're on
	req, _ := http.NewRequest("GET", fmt.Sprintf("/containers/%s/json", hostname), nil)
	req.URL.Scheme = "http"
	req.URL.Host = "docker"

	rec := &responseRecorder{header: make(http.Header)}
	dp.upstream.ServeHTTP(rec, req)

	if rec.code != http.StatusOK {
		return fmt.Errorf("inspect self (%s): HTTP %d: %s", hostname, rec.code, rec.body.String())
	}

	var info struct {
		NetworkSettings struct {
			Networks map[string]struct {
				NetworkID string `json:"NetworkID"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := json.Unmarshal(rec.body.Bytes(), &info); err != nil {
		return fmt.Errorf("parse container inspect: %w", err)
	}

	// Find the network matching our configured name
	net, ok := info.NetworkSettings.Networks[dp.cfg.NetworkName]
	if !ok {
		// List available networks for debugging
		available := make([]string, 0, len(info.NetworkSettings.Networks))
		for name := range info.NetworkSettings.Networks {
			available = append(available, name)
		}
		return fmt.Errorf("sandbox network %q not found on this container; available: %v",
			dp.cfg.NetworkName, available)
	}

	dp.cfg.NetworkID = net.NetworkID
	slog.Info("discovered sandbox network",
		"name", dp.cfg.NetworkName,
		"id", dp.cfg.NetworkID[:min(12, len(dp.cfg.NetworkID))],
	)
	return nil
}

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
