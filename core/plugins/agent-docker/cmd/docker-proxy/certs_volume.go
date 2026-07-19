package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const certsDir = "/shared/certs"

// EnsureCertsVolume creates and populates the certs volume that gets mounted
// into spawned containers for transparent proxy setup. This runs once at startup.
//
// The volume contains gateway-route.sh (networking setup) and ca.crt (MITM CA).
// Without this, spawned containers fail with "/shared/certs/gateway-route.sh: not found".
func (dp *DockerProxy) EnsureCertsVolume() error {
	volName := dp.mutator.certsVolumeName()

	// 1. Create volume (idempotent — Docker returns 200 if it already exists)
	if err := dp.createVolume(volName); err != nil {
		return fmt.Errorf("create certs volume %q: %w", volName, err)
	}

	// 2. Populate volume using a temporary container + archive upload
	if err := dp.populateCertsVolume(volName); err != nil {
		return fmt.Errorf("populate certs volume %q: %w", volName, err)
	}

	slog.Info("certs volume ready", "volume", volName)
	return nil
}

// createVolume creates a Docker volume with the given name. Idempotent.
func (dp *DockerProxy) createVolume(name string) error {
	body, _ := json.Marshal(map[string]any{
		"Name": name,
		"Labels": map[string]string{
			"agent-sandbox.sandbox":  dp.cfg.SandboxID,
			"agent-sandbox.managed":  "certs-volume",
		},
	})

	req, err := http.NewRequest("POST", "/volumes/create", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.URL.Scheme = "http"
	req.URL.Host = "docker"
	req.Header.Set("Content-Type", "application/json")

	rec := &responseRecorder{header: make(http.Header)}
	dp.upstream.ServeHTTP(rec, req)

	if rec.code != http.StatusCreated && rec.code != http.StatusOK {
		return fmt.Errorf("unexpected status %d: %s", rec.code, rec.body.String())
	}
	return nil
}

// populateCertsVolume uploads cert files into the volume using a temp container.
func (dp *DockerProxy) populateCertsVolume(volName string) error {
	// Create a minimal container that mounts the volume
	containerID, err := dp.createTempContainer(volName)
	if err != nil {
		return fmt.Errorf("create temp container: %w", err)
	}
	defer dp.removeTempContainer(containerID)

	// Build tar archive from /shared/certs/ files
	archive, err := buildCertsArchive()
	if err != nil {
		return fmt.Errorf("build archive: %w", err)
	}

	// Upload archive to container at /certs (the volume mount point)
	if err := dp.uploadArchive(containerID, "/certs", archive); err != nil {
		return fmt.Errorf("upload archive: %w", err)
	}

	return nil
}

// createTempContainer creates a non-running container with the certs volume mounted.
// Tries busybox first (tiny), falls back to alpine (likely cached from proxy image).
func (dp *DockerProxy) createTempContainer(volName string) (string, error) {
	for _, image := range []string{"busybox:latest", "alpine:latest"} {
		id, err := dp.tryCreateContainer(volName, image)
		if err == nil {
			return id, nil
		}
		// If image not found, try pulling it
		if strings.Contains(err.Error(), "No such image") {
			if pullErr := dp.pullImage(image); pullErr == nil {
				id, err = dp.tryCreateContainer(volName, image)
				if err == nil {
					return id, nil
				}
			}
		}
		slog.Debug("temp container image failed", "image", image, "error", err)
	}
	return "", fmt.Errorf("no suitable image available for temp container")
}

func (dp *DockerProxy) tryCreateContainer(volName, image string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"Image": image,
		"Cmd":   []string{"true"},
		"HostConfig": map[string]any{
			"Mounts": []map[string]any{
				{
					"Type":   "volume",
					"Source": volName,
					"Target": "/certs",
				},
			},
		},
		"Labels": map[string]string{
			"agent-sandbox.sandbox": dp.cfg.SandboxID,
			"agent-sandbox.managed": "certs-init",
		},
	})

	req, err := http.NewRequest("POST", "/containers/create", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.URL.Scheme = "http"
	req.URL.Host = "docker"
	req.Header.Set("Content-Type", "application/json")

	rec := &responseRecorder{header: make(http.Header)}
	dp.upstream.ServeHTTP(rec, req)

	if rec.code != http.StatusCreated {
		return "", fmt.Errorf("status %d: %s", rec.code, rec.body.String())
	}

	var result struct {
		Id string `json:"Id"`
	}
	if err := json.Unmarshal(rec.body.Bytes(), &result); err != nil {
		return "", err
	}
	return result.Id, nil
}

// pullImage pulls an image from the registry.
func (dp *DockerProxy) pullImage(image string) error {
	req, err := http.NewRequest("POST", fmt.Sprintf("/images/create?fromImage=%s", image), nil)
	if err != nil {
		return err
	}
	req.URL.Scheme = "http"
	req.URL.Host = "docker"

	rec := &responseRecorder{header: make(http.Header)}
	dp.upstream.ServeHTTP(rec, req)

	if rec.code != http.StatusOK {
		return fmt.Errorf("pull %s: status %d", image, rec.code)
	}
	return nil
}

// uploadArchive uploads a tar archive to a container path via Docker API.
func (dp *DockerProxy) uploadArchive(containerID, path string, archive []byte) error {
	req, err := http.NewRequest("PUT",
		fmt.Sprintf("/containers/%s/archive?path=%s", containerID, path),
		bytes.NewReader(archive))
	if err != nil {
		return err
	}
	req.URL.Scheme = "http"
	req.URL.Host = "docker"
	req.Header.Set("Content-Type", "application/x-tar")

	rec := &responseRecorder{header: make(http.Header)}
	dp.upstream.ServeHTTP(rec, req)

	if rec.code != http.StatusOK {
		return fmt.Errorf("status %d: %s", rec.code, rec.body.String())
	}
	return nil
}

// removeTempContainer removes the temporary container used for volume population.
func (dp *DockerProxy) removeTempContainer(containerID string) {
	req, _ := http.NewRequest("DELETE", fmt.Sprintf("/containers/%s?force=true", containerID), nil)
	req.URL.Scheme = "http"
	req.URL.Host = "docker"

	rec := &responseRecorder{header: make(http.Header)}
	dp.upstream.ServeHTTP(rec, req)

	if rec.code != http.StatusNoContent && rec.code != http.StatusOK {
		logID := containerID
		if len(logID) > 12 {
			logID = logID[:12]
		}
		slog.Warn("failed to remove temp container", "id", logID, "code", rec.code)
	}
}

// certsAllowList defines which files from /shared/certs/ are safe to
// expose to spawned containers. Private keys must NEVER be included.
var certsAllowList = map[string]bool{
	"gateway-route.sh": true,
	"ca.crt":           true,
}

// buildCertsArchive creates a tar archive containing only safe files from /shared/certs/.
// Private keys (ca.key) are explicitly excluded.
func buildCertsArchive() ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	entries, err := os.ReadDir(certsDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", certsDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		// Only include explicitly allowed files (never private keys)
		if !certsAllowList[entry.Name()] {
			continue
		}

		path := filepath.Join(certsDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}

		// Ensure scripts are executable
		mode := info.Mode()
		if strings.HasSuffix(entry.Name(), ".sh") {
			mode |= 0111
		}

		hdr := &tar.Header{
			Name: entry.Name(),
			Mode: int64(mode),
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := io.Copy(tw, bytes.NewReader(data)); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
