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
	dockerAddr string // "unix" for real socket, or "http://..." for testing
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
			slog.Warn("cleanup: stop container", "id", id[:min(12, len(id))], "error", err)
		} else {
			stopResp.Body.Close()
		}

		// Remove
		removeURL := fmt.Sprintf("%s/containers/%s?force=true", base, id)
		req, _ = http.NewRequestWithContext(ctx, "DELETE", removeURL, nil)
		rmResp, err := client.Do(req)
		if err != nil {
			slog.Warn("cleanup: remove container", "id", id[:min(12, len(id))], "error", err)
		} else {
			rmResp.Body.Close()
		}

		slog.Info("cleanup: removed container", "id", id[:min(12, len(id))], "name", name)
	}
}
