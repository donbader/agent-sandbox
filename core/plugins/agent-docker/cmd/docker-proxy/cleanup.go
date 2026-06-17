package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
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

// CleanupAll stops and removes all containers and networks labeled with this sandbox ID.
func (c *Cleaner) CleanupAll(ctx context.Context) {
	client := c.httpClient()
	base := c.baseURL()

	// Clean up containers first
	c.cleanupContainers(ctx, client, base)

	// Then clean up networks
	c.cleanupNetworks(ctx, client, base)
}

func (c *Cleaner) cleanupContainers(ctx context.Context, client *http.Client, base string) {
	filters := fmt.Sprintf(`{"label":["agent-sandbox.sandbox=%s"]}`, c.sandboxID)
	listURL := fmt.Sprintf("%s/containers/json?all=true&filters=%s", base, filters)

	resp, err := client.Get(listURL)
	if err != nil {
		slog.Error("cleanup: list containers", "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

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

	// Stop and remove in parallel to stay within Docker's SIGTERM grace period
	var wg sync.WaitGroup
	for _, container := range containers {
		wg.Add(1)
		go func(id string, names []string) {
			defer wg.Done()
			name := ""
			if len(names) > 0 {
				name = names[0]
			}

			// Force remove (kills + removes in one call)
			removeURL := fmt.Sprintf("%s/containers/%s?force=true", base, id)
			req, _ := http.NewRequestWithContext(ctx, "DELETE", removeURL, nil)
			rmResp, err := client.Do(req)
			if err != nil {
				slog.Warn("cleanup: remove container", "id", id[:min(12, len(id))], "error", err)
			} else {
				_ = rmResp.Body.Close()
			}

			slog.Info("cleanup: removed container", "id", id[:min(12, len(id))], "name", name)
		}(container.Id, container.Names)
	}
	wg.Wait()
}

func (c *Cleaner) cleanupNetworks(ctx context.Context, client *http.Client, base string) {
	filters := fmt.Sprintf(`{"label":["agent-sandbox.sandbox=%s"]}`, c.sandboxID)
	listURL := fmt.Sprintf("%s/networks?filters=%s", base, filters)

	resp, err := client.Get(listURL)
	if err != nil {
		slog.Error("cleanup: list networks", "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	var networks []struct {
		Id   string `json:"Id"`
		Name string `json:"Name"`
	}
	if err := json.Unmarshal(body, &networks); err != nil {
		slog.Error("cleanup: parse network list", "error", err)
		return
	}

	if len(networks) == 0 {
		return
	}

	slog.Info("cleanup: found networks", "count", len(networks))

	for _, network := range networks {
		removeURL := fmt.Sprintf("%s/networks/%s", base, network.Id)
		req, _ := http.NewRequestWithContext(ctx, "DELETE", removeURL, nil)
		rmResp, err := client.Do(req)
		if err != nil {
			slog.Warn("cleanup: remove network", "id", network.Id[:min(12, len(network.Id))], "error", err)
		} else {
			_ = rmResp.Body.Close()
		}

		slog.Info("cleanup: removed network", "id", network.Id[:min(12, len(network.Id))], "name", network.Name)
	}
}
