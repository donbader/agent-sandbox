package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// DockerVersion holds the Docker daemon version info.
type DockerVersion struct {
	Version    string `json:"Version"`
	APIVersion string `json:"ApiVersion"`
}

// SupportsVolumeSubpath returns true if the Docker API version is >= 1.45 (Engine 26+).
func (v *DockerVersion) SupportsVolumeSubpath() bool {
	parts := strings.SplitN(v.APIVersion, ".", 2)
	if len(parts) != 2 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	// volume-subpath requires API 1.45+ (Docker Engine 26.0+)
	return major > 1 || (major == 1 && minor >= 45)
}

// checkDockerVersion queries the Docker daemon for its version.
func (dp *DockerProxy) checkDockerVersion() *DockerVersion {
	req, err := http.NewRequest("GET", "/version", nil)
	if err != nil {
		slog.Warn("checkDockerVersion: failed to create request", "error", err)
		return nil
	}
	req.URL.Scheme = "http"
	req.URL.Host = "docker"

	rec := &responseRecorder{header: make(http.Header)}
	dp.upstream.ServeHTTP(rec, req)

	if rec.code != http.StatusOK {
		slog.Warn("checkDockerVersion: daemon returned non-200", "code", rec.code)
		return nil
	}

	var ver DockerVersion
	if err := json.Unmarshal(rec.body.Bytes(), &ver); err != nil {
		slog.Warn("checkDockerVersion: failed to parse response", "error", err)
		return nil
	}
	return &ver
}
