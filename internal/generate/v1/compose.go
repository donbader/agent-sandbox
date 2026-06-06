package v1

import (
	"fmt"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/envvar"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"gopkg.in/yaml.v3"
)

type composeFile struct {
	Services map[string]any `yaml:"services"`
	Volumes  map[string]any `yaml:"volumes,omitempty"`
	Networks map[string]any `yaml:"networks,omitempty"`
}

// BuildCompose generates a docker-compose.yml string from config and plugin contributions.
// projectDir is used to compute relative paths for sidecar build contexts.
func BuildCompose(cfg *config.Config, contribs *plugin.Contributions, projectDir string) (string, error) {
	compose := composeFile{
		Services: map[string]any{},
		Volumes:  map[string]any{},
		Networks: map[string]any{},
	}

	buildDir := filepath.Join(projectDir, ".build")

	// Agent service
	// cap_add NET_ADMIN is required for iptables DNAT rules in entrypoint.sh.
	agentName := cfg.Name
	gatewayName := cfg.Name + "-gateway"

	agentVolumes := []string{"certs:/shared/certs"}
	agentVolumes = append(agentVolumes, cfg.Runtime.Volumes...)
	if contribs != nil {
		agentVolumes = append(agentVolumes, contribs.Runtime.Volumes...)
	}
	agentSvc := map[string]any{
		"build": map[string]any{
			"context":    "..",
			"dockerfile": ".build/Dockerfile",
		},
		"cap_add": []string{"NET_ADMIN"},
		"depends_on": map[string]any{
			gatewayName: map[string]any{
				"condition": "service_healthy",
			},
		},
		"networks": map[string]any{
			"sandbox": map[string]any{
				"aliases": []string{"agent"},
			},
		},
		"volumes": agentVolumes,
	}
	// Add healthcheck if the agent exposes ports (agent-manager listens on the first declared port).
	if contribs != nil && len(contribs.Runtime.Ports) > 0 {
		port := contribs.Runtime.Ports[0]
		agentSvc["healthcheck"] = map[string]any{
			"test":     []string{"CMD", "curl", "-sf", fmt.Sprintf("http://localhost:%s/health", port)},
			"interval": "3s",
			"timeout":  "3s",
			"retries":  5,
		}
	}
	// Expose ports from plugin contributions (e.g. SSH)
	if contribs != nil && len(contribs.Runtime.Ports) > 0 {
		agentSvc["ports"] = contribs.Runtime.Ports
	}
	compose.Services[agentName] = agentSvc

	// Gateway service
	// The gateway writes /shared/certs/ca.crt so the agent can install it.
	gatewayEnv := collectGatewayEnvVars(cfg, contribs)
	gatewaySvc := map[string]any{
		"build": map[string]any{
			"context":    "./gateway-src",
			"dockerfile": "Dockerfile",
		},
		"networks": map[string]any{
			"sandbox": map[string]any{
				"aliases": []string{"gateway"},
			},
		},
		"volumes": []string{"certs:/shared/certs"},
		"healthcheck": map[string]any{
			"test":     []string{"CMD", "wget", "--spider", "-q", "http://localhost:8080/health"},
			"interval": "5s",
			"timeout":  "3s",
			"retries":  3,
		},
	}
	if len(gatewayEnv) > 0 {
		gatewaySvc["environment"] = gatewayEnv
	}
	compose.Services[gatewayName] = gatewaySvc

	// Sidecar services from plugins
	if contribs != nil {
		for name, svc := range contribs.Sidecar.Services {
			sidecar := buildSidecarService(svc, buildDir)
			// Sidecars implicitly depend on the agent service being started.
			if sidecar["depends_on"] == nil {
				sidecar["depends_on"] = map[string]any{
					agentName: map[string]any{
						"condition": "service_healthy",
					},
				}
			}
			compose.Services[name] = sidecar
		}
	}

	// Sandbox network
	compose.Networks["sandbox"] = map[string]any{"driver": "bridge"}

	// The certs volume is always present — shared between gateway (writer) and agent (reader).
	compose.Volumes["certs"] = nil

	// Extract any additional named volumes from user config
	for _, v := range cfg.Runtime.Volumes {
		volName := extractVolumeName(v)
		if volName != "" {
			compose.Volumes[volName] = nil
		}
	}

	// Extract named volumes from plugin runtime contributions
	if contribs != nil {
		for _, v := range contribs.Runtime.Volumes {
			volName := extractVolumeName(v)
			if volName != "" {
				compose.Volumes[volName] = nil
			}
		}
	}

	data, err := yaml.Marshal(compose)
	if err != nil {
		return "", fmt.Errorf("marshal compose: %w", err)
	}
	return string(data), nil
}

// buildSidecarService constructs the compose service definition for a plugin sidecar.
func buildSidecarService(svc plugin.ComposeService, buildDir string) map[string]any {
	s := map[string]any{
		"networks": []string{"sandbox"},
	}
	if svc.Build != "" {
		relPath, err := filepath.Rel(buildDir, svc.Build)
		if err != nil {
			relPath = svc.Build
		}
		s["build"] = relPath
	}
	if svc.Image != "" {
		s["image"] = svc.Image
	}
	if len(svc.Environment) > 0 {
		s["environment"] = svc.Environment
	}
	if len(svc.Volumes) > 0 {
		s["volumes"] = svc.Volumes
	}
	if len(svc.Ports) > 0 {
		s["ports"] = svc.Ports
	}
	if svc.Healthcheck != nil {
		s["healthcheck"] = svc.Healthcheck
	}
	if svc.DependsOn != nil {
		s["depends_on"] = svc.DependsOn
	}
	return s
}

func extractVolumeName(volume string) string {
	// Named volumes have format "name:/path" (no leading . or /)
	for i, c := range volume {
		if c == ':' {
			name := volume[:i]
			if len(name) > 0 && name[0] != '.' && name[0] != '/' {
				return name
			}
			return ""
		}
	}
	return ""
}

// collectGatewayEnvVars extracts env var names referenced in gateway service headers
// and returns them as docker-compose environment entries (passthrough format).
// Note: middleware env vars are NOT included here — middleware code gets secrets
// baked in at generate-time via template rendering.
func collectGatewayEnvVars(cfg *config.Config, contribs *plugin.Contributions) []string {
	seen := map[string]bool{}

	// From user gateway config
	for _, svc := range cfg.Gateway.Services {
		for _, value := range svc.Headers {
			if ev := envvar.Extract(value); ev != "" {
				seen[ev] = true
			}
		}
	}

	// From plugin contributions (header-based only)
	if contribs != nil {
		for _, svc := range contribs.Gateway.Services {
			for _, value := range svc.Headers {
				if ev := envvar.Extract(value); ev != "" {
					seen[ev] = true
				}
			}
		}
	}

	var envVars []string
	for v := range seen {
		envVars = append(envVars, v)
	}
	return envVars
}

