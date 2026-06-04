package v1

import (
	"fmt"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/config"
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
func BuildCompose(cfg *config.V1Config, contribs *plugin.Contributions, projectDir string) (string, error) {
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
		"networks": []string{"sandbox"},
		"volumes":  agentVolumes,
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
			sidecarSvc := map[string]any{
				"networks": []string{"sandbox"},
			}
			if svc.Build != "" {
				// Make build path relative to .build/ directory
				relPath, err := filepath.Rel(buildDir, svc.Build)
				if err != nil {
					relPath = svc.Build
				}
				sidecarSvc["build"] = relPath
			}
			if svc.Image != "" {
				sidecarSvc["image"] = svc.Image
			}
			if len(svc.Environment) > 0 {
				sidecarSvc["environment"] = svc.Environment
			}
			if len(svc.Volumes) > 0 {
				sidecarSvc["volumes"] = svc.Volumes
			}
			if len(svc.Ports) > 0 {
				sidecarSvc["ports"] = svc.Ports
			}
			if svc.Healthcheck != nil {
				sidecarSvc["healthcheck"] = svc.Healthcheck
			}
			if svc.DependsOn != nil {
				sidecarSvc["depends_on"] = svc.DependsOn
			}
			compose.Services[name] = sidecarSvc
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

	data, err := yaml.Marshal(compose)
	if err != nil {
		return "", fmt.Errorf("marshal compose: %w", err)
	}
	return string(data), nil
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
func collectGatewayEnvVars(cfg *config.V1Config, contribs *plugin.Contributions) []string {
	seen := map[string]bool{}

	// From user gateway config
	for _, svc := range cfg.Gateway.Services {
		for _, value := range svc.Headers {
			if envVar := extractEnvVar(value); envVar != "" {
				seen[envVar] = true
			}
		}
	}

	// From plugin contributions (header-based only)
	if contribs != nil {
		for _, svc := range contribs.Gateway.Services {
			for _, value := range svc.Headers {
				if envVar := extractEnvVar(value); envVar != "" {
					seen[envVar] = true
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

// extractEnvVar finds ${VAR_NAME} in a string and returns the var name.
func extractEnvVar(s string) string {
	start := -1
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '$' && s[i+1] == '{' {
			start = i + 2
			break
		}
	}
	if start == -1 {
		return ""
	}
	for i := start; i < len(s); i++ {
		if s[i] == '}' {
			return s[start:i]
		}
	}
	return ""
}
