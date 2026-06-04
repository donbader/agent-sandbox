package v1

import (
	"fmt"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"gopkg.in/yaml.v3"
)

type composeFile struct {
	Services map[string]any `yaml:"services"`
	Volumes  map[string]any `yaml:"volumes,omitempty"`
	Networks map[string]any `yaml:"networks,omitempty"`
}

// BuildCompose generates a docker-compose.yaml string from config and plugin contributions.
func BuildCompose(cfg *config.V1Config, contribs *plugin.Contributions) (string, error) {
	compose := composeFile{
		Services: map[string]any{},
		Volumes:  map[string]any{},
		Networks: map[string]any{},
	}

	// Agent service
	agentSvc := map[string]any{
		"build": map[string]any{
			"context":    ".",
			"dockerfile": "Dockerfile",
		},
		"depends_on": []string{"gateway"},
		"networks":   []string{"sandbox"},
	}
	if len(cfg.Runtime.Volumes) > 0 {
		agentSvc["volumes"] = cfg.Runtime.Volumes
	}
	compose.Services["agent"] = agentSvc

	// Gateway service
	gatewaySvc := map[string]any{
		"build": map[string]any{
			"context":    "./gateway-src",
			"dockerfile": "Dockerfile",
		},
		"networks": []string{"sandbox"},
		"healthcheck": map[string]any{
			"test":     []string{"CMD", "wget", "--spider", "-q", "http://localhost:8080/health"},
			"interval": "5s",
			"timeout":  "3s",
			"retries":  3,
		},
	}
	compose.Services["gateway"] = gatewaySvc

	// Sidecar services from plugins
	if contribs != nil {
		for name, svc := range contribs.Sidecar.Services {
			sidecarSvc := map[string]any{
				"networks": []string{"sandbox"},
			}
			if svc.Build != "" {
				sidecarSvc["build"] = svc.Build
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

	// Extract named volumes
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
