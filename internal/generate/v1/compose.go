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
		"cap_drop": []string{"ALL"},
		"cap_add":  []string{"NET_ADMIN", "SETUID", "SETGID", "DAC_OVERRIDE", "CHOWN", "FOWNER"},
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
		"environment": map[string]string{
			"GATEWAY_HOST": "gateway",
		},
	}
	// Merge user-defined runtime.environment into agent service env.
	if len(cfg.Runtime.Environment) > 0 {
		if envMap, ok := agentSvc["environment"].(map[string]string); ok {
			for k, v := range cfg.Runtime.Environment {
				envMap[k] = v
			}
		}
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
	// Podman rootless requires userns_mode: keep-id for file ownership mapping
	if cfg.RuntimeEngine == "podman" {
		agentSvc["userns_mode"] = "keep-id"
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
		"cap_drop": []string{"ALL"},
		"cap_add":  []string{"NET_ADMIN", "NET_BIND_SERVICE"},
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
	// Expose gateway HTTP port when plugin routes are registered (e.g. OAuth callbacks).
	// Use "8080" (no host port) so Docker assigns a random available host port,
	// avoiding conflicts when multiple agent-sandbox instances run on the same host.
	if contribs != nil && len(contribs.Gateway.Routes) > 0 {
		gatewaySvc["ports"] = []string{"8080"}
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

// ComposeAgentEntry holds the data needed to generate one agent's services in a fleet compose file.
type ComposeAgentEntry struct {
	Config   *config.Config
	Contribs *plugin.Contributions
	BuildDir string // absolute path to the agent's .build/<name>/ directory
}

// BuildFleetCompose generates a unified docker-compose.yml for multiple agents.
// Each agent gets its own service + gateway, sharing a single sandbox network.
func BuildFleetCompose(agents []ComposeAgentEntry, projectDir string) (string, error) {
	compose := composeFile{
		Services: map[string]any{},
		Volumes:  map[string]any{},
		Networks: map[string]any{},
	}

	// Shared network for all agents
	compose.Networks["sandbox"] = map[string]any{"driver": "bridge"}

	for _, agent := range agents {
		cfg := agent.Config
		agentName := cfg.Name
		gatewayName := cfg.Name + "-gateway"
		certsVolume := agentName + "-certs"

		// Each agent-gateway pair gets its own certs volume to avoid CA key conflicts.
		compose.Volumes[certsVolume] = nil

		// Relative build dir from .build/ (e.g., "./<agent-name>")
		relBuildDir, err := filepath.Rel(filepath.Join(projectDir, ".build"), agent.BuildDir)
		if err != nil {
			relBuildDir = agent.BuildDir
		}

		// Agent service
		agentVolumes := []string{certsVolume + ":/shared/certs"}
		agentVolumes = append(agentVolumes, cfg.Runtime.Volumes...)
		if agent.Contribs != nil {
			agentVolumes = append(agentVolumes, agent.Contribs.Runtime.Volumes...)
		}

		agentSvc := map[string]any{
			"build": map[string]any{
				"context":    "..",
				"dockerfile": filepath.Join(".build", relBuildDir, "Dockerfile"),
			},
			"cap_drop": []string{"ALL"},
			"cap_add":  []string{"NET_ADMIN", "SETUID", "SETGID", "DAC_OVERRIDE", "CHOWN", "FOWNER"},
			"depends_on": map[string]any{
				gatewayName: map[string]any{
					"condition": "service_healthy",
				},
			},
			"networks": map[string]any{
				"sandbox": map[string]any{
					"aliases": []string{agentName},
				},
			},
			"volumes": agentVolumes,
			"environment": map[string]string{
				"GATEWAY_HOST": gatewayName,
			},
		}

		// Merge user-defined runtime.environment into agent service env.
		if len(cfg.Runtime.Environment) > 0 {
			if envMap, ok := agentSvc["environment"].(map[string]string); ok {
				for k, v := range cfg.Runtime.Environment {
					envMap[k] = v
				}
			}
		}

		if agent.Contribs != nil && len(agent.Contribs.Runtime.Ports) > 0 {
			port := agent.Contribs.Runtime.Ports[0]
			agentSvc["healthcheck"] = map[string]any{
				"test":     []string{"CMD", "curl", "-sf", fmt.Sprintf("http://localhost:%s/health", port)},
				"interval": "3s",
				"timeout":  "3s",
				"retries":  5,
			}
			agentSvc["ports"] = agent.Contribs.Runtime.Ports
		}

		if cfg.RuntimeEngine == "podman" {
			agentSvc["userns_mode"] = "keep-id"
		}

		compose.Services[agentName] = agentSvc

		// Gateway service (per-agent build context for agent-specific middleware)
		gatewayEnv := collectGatewayEnvVars(cfg, agent.Contribs)
		gatewaySvc := map[string]any{
			"build": map[string]any{
				"context":    fmt.Sprintf("./%s/gateway-src", relBuildDir),
				"dockerfile": "Dockerfile",
			},
			"cap_drop": []string{"ALL"},
			"cap_add":  []string{"NET_ADMIN", "NET_BIND_SERVICE"},
			"networks": map[string]any{
				"sandbox": map[string]any{
					"aliases": []string{gatewayName},
				},
			},
			"volumes": []string{
				certsVolume + ":/shared/certs",
				fmt.Sprintf("./%s/config.yaml:/etc/gateway/config.yaml:ro", relBuildDir),
			},
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

		// Sidecar services
		// Paths must be relative to .build/ (where docker-compose.yml lives),
		// not .build/<agent>/ (the per-agent build dir).
		if agent.Contribs != nil {
			composeDir := filepath.Join(projectDir, ".build")
			for name, svc := range agent.Contribs.Sidecar.Services {
				sidecar := buildSidecarService(svc, composeDir)
				if sidecar["depends_on"] == nil {
					sidecar["depends_on"] = map[string]any{
						agentName: map[string]any{
							"condition": "service_healthy",
						},
					}
				}
				// Prefix sidecar name with agent name to avoid collisions
				sidecarName := agentName + "-" + name
				compose.Services[sidecarName] = sidecar
			}
		}

		// Extract named volumes
		for _, v := range cfg.Runtime.Volumes {
			volName := extractVolumeName(v)
			if volName != "" {
				compose.Volumes[volName] = nil
			}
		}
		if agent.Contribs != nil {
			for _, v := range agent.Contribs.Runtime.Volumes {
				volName := extractVolumeName(v)
				if volName != "" {
					compose.Volumes[volName] = nil
				}
			}
		}
	}

	data, err := yaml.Marshal(compose)
	if err != nil {
		return "", fmt.Errorf("marshal fleet compose: %w", err)
	}
	return string(data), nil
}

