package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// VolumeMount represents a named volume mount on the agent container.
type VolumeMount struct {
	ContainerPath string // e.g. "/home/agent"
	VolumeName    string // e.g. "my-project-coder-home"
}

// VolumeTranslator handles translating bind mount paths to volume-subpath mounts.
type VolumeTranslator struct {
	mounts             []VolumeMount
	supportsSubpath    bool
	dockerVersion      string
}

// NewVolumeTranslator creates a translator by inspecting the agent container's mounts.
func (dp *DockerProxy) NewVolumeTranslator() *VolumeTranslator {
	vt := &VolumeTranslator{}

	// Check Docker version
	ver := dp.checkDockerVersion()
	if ver != nil {
		vt.supportsSubpath = ver.SupportsVolumeSubpath()
		vt.dockerVersion = ver.Version
		slog.Info("docker version", "version", ver.Version, "api", ver.APIVersion, "volume_subpath", vt.supportsSubpath)
	}

	if !vt.supportsSubpath {
		slog.Warn("volume-subpath not supported (requires Docker Engine 26+ / API 1.45+), bind mount translation disabled")
		return vt
	}

	// Discover agent container's volume mounts
	mounts := dp.discoverAgentMounts()
	if len(mounts) == 0 {
		slog.Info("no named volume mounts found on agent container")
	} else {
		vt.mounts = mounts
		for _, m := range mounts {
			slog.Info("volume mount discovered", "path", m.ContainerPath, "volume", m.VolumeName)
		}
	}

	return vt
}

// discoverAgentMounts finds the agent container and returns its named volume mounts.
func (dp *DockerProxy) discoverAgentMounts() []VolumeMount {
	// Find agent container by sandbox label
	filters := url.QueryEscape(fmt.Sprintf(`{"label":["agent-sandbox.sandbox=%s","agent-sandbox.agent=%s"]}`, dp.cfg.SandboxID, dp.cfg.AgentName))
	req, err := http.NewRequest("GET", fmt.Sprintf("/containers/json?filters=%s", filters), nil)
	if err != nil {
		slog.Debug("discoverAgentMounts: failed to create request", "error", err)
		return nil
	}
	req.URL.Scheme = "http"
	req.URL.Host = "docker"

	rec := &responseRecorder{header: make(http.Header)}
	dp.upstream.ServeHTTP(rec, req)

	if rec.code != http.StatusOK {
		slog.Debug("discoverAgentMounts: container list failed", "code", rec.code)
		return nil
	}

	var containers []struct {
		Id string `json:"Id"`
	}
	if err := json.Unmarshal(rec.body.Bytes(), &containers); err != nil || len(containers) == 0 {
		slog.Debug("discoverAgentMounts: no agent container found")
		return nil
	}

	// Inspect the first matching container
	containerID := containers[0].Id
	inspReq, err := http.NewRequest("GET", fmt.Sprintf("/containers/%s/json", containerID), nil)
	if err != nil {
		return nil
	}
	inspReq.URL.Scheme = "http"
	inspReq.URL.Host = "docker"

	inspRec := &responseRecorder{header: make(http.Header)}
	dp.upstream.ServeHTTP(inspRec, inspReq)

	if inspRec.code != http.StatusOK {
		slog.Debug("discoverAgentMounts: container inspect failed", "code", inspRec.code)
		return nil
	}

	var info struct {
		Mounts []struct {
			Type        string `json:"Type"`
			Name        string `json:"Name"`
			Destination string `json:"Destination"`
		} `json:"Mounts"`
	}
	if err := json.Unmarshal(inspRec.body.Bytes(), &info); err != nil {
		slog.Debug("discoverAgentMounts: failed to parse inspect", "error", err)
		return nil
	}

	var mounts []VolumeMount
	for _, m := range info.Mounts {
		if m.Type == "volume" && m.Name != "" {
			mounts = append(mounts, VolumeMount{
				ContainerPath: m.Destination,
				VolumeName:    m.Name,
			})
		}
	}

	// Sort by path length descending so longer prefixes match first
	for i := 0; i < len(mounts); i++ {
		for j := i + 1; j < len(mounts); j++ {
			if len(mounts[j].ContainerPath) > len(mounts[i].ContainerPath) {
				mounts[i], mounts[j] = mounts[j], mounts[i]
			}
		}
	}

	return mounts
}

// TranslateBinds converts bind mounts that fall under known volumes to
// Docker volume-subpath Mounts. Returns the modified Binds and new Mounts to add.
// Returns an error if a host bind mount doesn't match any volume.
func (vt *VolumeTranslator) TranslateBinds(binds []string) (remainingBinds []string, mounts []map[string]any, err error) {
	for _, bind := range binds {
		parts := strings.SplitN(bind, ":", 3)
		src := parts[0]

		if !strings.HasPrefix(src, "/") {
			// Named volume mount (e.g. "myvolume:/data") — pass through
			remainingBinds = append(remainingBinds, bind)
			continue
		}

		// Host path bind mount — try to translate
		target := ""
		readOnly := false
		if len(parts) >= 2 {
			target = parts[1]
		}
		if len(parts) >= 3 && parts[2] == "ro" {
			readOnly = true
		}

		mount, matched := vt.matchVolume(src, target, readOnly)
		if matched {
			mounts = append(mounts, mount)
		} else {
			if !vt.supportsSubpath {
				err = fmt.Errorf("bind mount %q requires volume-subpath translation (Docker Engine 26+, current: %s)", src, vt.dockerVersion)
			} else {
				err = fmt.Errorf("bind mount %q is not under any shared volume on the agent container", src)
			}
			return
		}
	}
	return
}

// matchVolume checks if a host path falls under a known volume mount and returns
// the translated Docker Mount spec.
func (vt *VolumeTranslator) matchVolume(src, target string, readOnly bool) (map[string]any, bool) {
	for _, vm := range vt.mounts {
		prefix := vm.ContainerPath
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}

		if src == vm.ContainerPath || strings.HasPrefix(src, prefix) {
			subpath := ""
			if src != vm.ContainerPath {
				subpath = strings.TrimPrefix(src, prefix)
			}

			mount := map[string]any{
				"Type":   "volume",
				"Source": vm.VolumeName,
				"Target": target,
			}

			volumeOpts := map[string]any{}
			if subpath != "" {
				volumeOpts["Subpath"] = subpath
			}
			if len(volumeOpts) > 0 {
				mount["VolumeOptions"] = volumeOpts
			}

			if readOnly {
				mount["ReadOnly"] = true
			}

			return mount, true
		}
	}
	return nil, false
}

// translateBindMounts modifies the container create body in-place:
// converts host bind mounts under known volumes to volume-subpath Mounts.
func (dp *DockerProxy) translateBindMounts(body map[string]any) error {
	hc, ok := body["HostConfig"].(map[string]any)
	if !ok || hc == nil {
		return nil
	}

	bindsRaw, ok := hc["Binds"].([]any)
	if !ok || len(bindsRaw) == 0 {
		return nil
	}

	var binds []string
	for _, b := range bindsRaw {
		if s, ok := b.(string); ok {
			binds = append(binds, s)
		}
	}

	// Intercept docker.sock mounts: remove them and inject DOCKER_HOST
	var filteredBinds []string
	hasSocketMount := false
	for _, b := range binds {
		if isDockerSocketBind(b) {
			hasSocketMount = true
		} else {
			filteredBinds = append(filteredBinds, b)
		}
	}
	if hasSocketMount {
		dp.injectDockerHost(body)
	}
	binds = filteredBinds

	remaining, newMounts, err := dp.volumes.TranslateBinds(binds)
	if err != nil {
		return err
	}

	// Update Binds (only named volumes remain)
	if len(remaining) > 0 {
		newBinds := make([]any, len(remaining))
		for i, b := range remaining {
			newBinds[i] = b
		}
		hc["Binds"] = newBinds
	} else {
		delete(hc, "Binds")
	}

	// Add translated mounts (Docker API: Mounts is top-level, not in HostConfig)
	if len(newMounts) > 0 {
		existing, _ := body["Mounts"].([]any)
		for _, m := range newMounts {
			existing = append(existing, m)
		}
		body["Mounts"] = existing
	}

	body["HostConfig"] = hc
	return nil
}

// isDockerSocketBind returns true if the bind mount source or target refers to a Docker socket.
func isDockerSocketBind(bind string) bool {
	parts := strings.SplitN(bind, ":", 3)
	src := parts[0]
	target := ""
	if len(parts) >= 2 {
		target = parts[1]
	}
	return isDockerSocketPath(src) || isDockerSocketPath(target)
}

// isDockerSocketPath returns true if the path looks like a Docker daemon socket.
func isDockerSocketPath(p string) bool {
	return p == "/var/run/docker.sock" ||
		p == "/run/docker.sock" ||
		strings.HasSuffix(p, "/docker.sock")
}

// injectDockerHost removes any existing DOCKER_HOST from env and adds one pointing to this proxy.
func (dp *DockerProxy) injectDockerHost(body map[string]any) {
	proxyHost := fmt.Sprintf("tcp://%s-agent-docker-proxy:2375", dp.cfg.AgentName)

	var env []any
	if existing, ok := body["Env"].([]any); ok {
		for _, e := range existing {
			s, _ := e.(string)
			if !strings.HasPrefix(s, "DOCKER_HOST=") {
				env = append(env, e)
			}
		}
	}
	env = append(env, "DOCKER_HOST="+proxyHost)
	body["Env"] = env
}
