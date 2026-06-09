package v1

import (
	"fmt"
	"maps"
	"path/filepath"
	"strings"

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

// agentPairParams holds the varying values between single-agent and fleet compose generation.
type agentPairParams struct {
	cfg            *config.Config
	contribs       *plugin.Contributions
	agentName      string
	gatewayName    string
	agentAlias     string
	gatewayAlias   string
	certsVolume    string
	agentBuild     map[string]any
	gatewayBuild   map[string]any
	gatewayVolumes []string
	sidecarPrefix  string
	buildDir       string
	exposeGateway  bool
}

// agentPairResult holds the services and volumes produced by buildAgentPair.
type agentPairResult struct {
	services map[string]any
	volumes  map[string]any
}

// buildAgentPair constructs the agent service, gateway service, and sidecar services
// for a single agent-gateway pair. Both BuildCompose and BuildFleetCompose delegate here.
func buildAgentPair(p agentPairParams) agentPairResult {
	result := agentPairResult{
		services: map[string]any{},
		volumes:  map[string]any{},
	}

	cfg := p.cfg
	contribs := p.contribs

	// Agent volumes: certs + user config + plugin contributions
	agentVolumes := []string{p.certsVolume + ":/shared/certs"}
	agentVolumes = append(agentVolumes, cfg.Runtime.Volumes...)
	if contribs != nil {
		agentVolumes = append(agentVolumes, contribs.Runtime.Volumes...)
	}

	// Build cap_add from base set plus plugin contributions.
	// cap_add NET_ADMIN is required for iptables DNAT rules in entrypoint.sh.
	baseCaps := []string{"NET_ADMIN", "SETUID", "SETGID", "DAC_OVERRIDE", "CHOWN", "FOWNER"}
	if contribs != nil {
		baseCaps = mergeCapabilities(baseCaps, contribs.Runtime.CapAdd)
	}

	agentSvc := map[string]any{
		"build":    p.agentBuild,
		"cap_drop": []string{"ALL"},
		"cap_add":  baseCaps,
		"depends_on": map[string]any{
			p.gatewayName: map[string]any{
				"condition": "service_healthy",
			},
		},
		"networks": map[string]any{
			"sandbox": map[string]any{
				"aliases": []string{p.agentAlias},
			},
		},
		"volumes": agentVolumes,
		"environment": map[string]string{
			"GATEWAY_HOST": p.gatewayAlias,
		},
	}

	// Merge user-defined runtime.environment into agent service env.
	if len(cfg.Runtime.Environment) > 0 {
		if envMap, ok := agentSvc["environment"].(map[string]string); ok {
			maps.Copy(envMap, cfg.Runtime.Environment)
		}
	}

	// Add healthcheck if the agent exposes ports (agent-manager listens on the first declared port).
	if contribs != nil && len(contribs.Runtime.Ports) > 0 {
		port := contribs.Runtime.Ports[0]
		if parts := strings.SplitN(port, ":", 2); len(parts) == 2 {
			port = parts[1]
		}
		agentSvc["healthcheck"] = map[string]any{
			"test":     []string{"CMD", "curl", "-sf", fmt.Sprintf("http://localhost:%s/health", port)},
			"interval": "3s",
			"timeout":  "3s",
			"retries":  5,
		}
		agentSvc["ports"] = contribs.Runtime.Ports
	}

	// Podman rootless requires userns_mode: keep-id for file ownership mapping.
	// Skip when a plugin declares skip_userns (e.g. sshd needs real root for privilege separation).
	skipUserns := contribs != nil && contribs.Runtime.SkipUserns
	if cfg.RuntimeEngine == "podman" && !skipUserns {
		agentSvc["userns_mode"] = "keep-id"
	}

	result.services[p.agentName] = agentSvc

	// Gateway service
	// The gateway writes /shared/certs/ca.crt so the agent can install it.
	gatewayEnv := collectGatewayEnvVars(cfg, contribs)
	gatewaySvc := map[string]any{
		"build":    p.gatewayBuild,
		"cap_drop": []string{"ALL"},
		"cap_add":  []string{"NET_ADMIN", "NET_BIND_SERVICE"},
		"networks": map[string]any{
			"sandbox": map[string]any{
				"aliases": []string{p.gatewayAlias},
			},
		},
		"volumes": p.gatewayVolumes,
		"healthcheck": map[string]any{
			"test":     []string{"CMD", "wget", "--spider", "-q", "http://localhost:8080/health"},
			"interval": "5s",
			"timeout":  "3s",
			"retries":  3,
		},
	}

	// Expose gateway HTTP port when plugin routes are registered (e.g. OAuth callbacks)
	if p.exposeGateway && contribs != nil && len(contribs.Gateway.Routes) > 0 {
		gatewaySvc["ports"] = []string{"8080:8080"}
	}
	if len(gatewayEnv) > 0 {
		gatewaySvc["environment"] = gatewayEnv
	}
	result.services[p.gatewayName] = gatewaySvc

	// Sidecar services from plugins
	if contribs != nil {
		for name, svc := range contribs.Sidecar.Services {
			sidecar := buildSidecarService(svc, p.buildDir)
			// Sidecars implicitly depend on the agent service being started.
			if sidecar["depends_on"] == nil {
				sidecar["depends_on"] = map[string]any{
					p.agentName: map[string]any{
						"condition": "service_healthy",
					},
				}
			}
			sidecarName := name
			if p.sidecarPrefix != "" {
				sidecarName = p.sidecarPrefix + "-" + name
			}
			result.services[sidecarName] = sidecar
		}
	}

	// Certs volume is always present — shared between gateway (writer) and agent (reader).
	result.volumes[p.certsVolume] = nil

	// Extract named volumes from user config
	for _, v := range cfg.Runtime.Volumes {
		if volName := extractVolumeName(v); volName != "" {
			result.volumes[volName] = nil
		}
	}

	// Extract named volumes from plugin runtime contributions
	if contribs != nil {
		for _, v := range contribs.Runtime.Volumes {
			if volName := extractVolumeName(v); volName != "" {
				result.volumes[volName] = nil
			}
		}
	}

	return result
}

// BuildCompose generates a docker-compose.yml string from config and plugin contributions.
// projectDir is used to compute relative paths for sidecar build contexts.
func BuildCompose(cfg *config.Config, contribs *plugin.Contributions, projectDir string) (string, error) {
	buildDir := filepath.Join(projectDir, ".build")

	pair := buildAgentPair(agentPairParams{
		cfg:          cfg,
		contribs:     contribs,
		agentName:    cfg.Name,
		gatewayName:  cfg.Name + "-gateway",
		agentAlias:   "agent",
		gatewayAlias: "gateway",
		certsVolume:  "certs",
		agentBuild: map[string]any{
			"context":    "..",
			"dockerfile": ".build/Dockerfile",
		},
		gatewayBuild: map[string]any{
			"context":    "./gateway-src",
			"dockerfile": "Dockerfile",
		},
		gatewayVolumes: []string{"certs:/shared/certs"},
		sidecarPrefix:  "",
		buildDir:       buildDir,
		exposeGateway:  true,
	})

	compose := composeFile{
		Services: pair.services,
		Volumes:  pair.volumes,
		Networks: map[string]any{"sandbox": map[string]any{"driver": "bridge"}},
	}

	data, err := yaml.Marshal(compose)
	if err != nil {
		return "", fmt.Errorf("marshal compose: %w", err)
	}
	return string(data), nil
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
		Networks: map[string]any{"sandbox": map[string]any{"driver": "bridge"}},
	}

	for _, agent := range agents {
		cfg := agent.Config
		agentName := cfg.Name
		gatewayName := cfg.Name + "-gateway"
		certsVolume := agentName + "-certs"

		// Relative build dir from .build/ (e.g., "./<agent-name>")
		relBuildDir, err := filepath.Rel(filepath.Join(projectDir, ".build"), agent.BuildDir)
		if err != nil {
			relBuildDir = agent.BuildDir
		}

		// Paths must be relative to .build/ (where docker-compose.yml lives),
		// not .build/<agent>/ (the per-agent build dir).
		composeDir := filepath.Join(projectDir, ".build")

		pair := buildAgentPair(agentPairParams{
			cfg:          cfg,
			contribs:     agent.Contribs,
			agentName:    agentName,
			gatewayName:  gatewayName,
			agentAlias:   agentName,
			gatewayAlias: gatewayName,
			certsVolume:  certsVolume,
			agentBuild: map[string]any{
				"context":    "..",
				"dockerfile": filepath.Join(".build", relBuildDir, "Dockerfile"),
			},
			gatewayBuild: map[string]any{
				"context":    fmt.Sprintf("./%s/gateway-src", relBuildDir),
				"dockerfile": "Dockerfile",
			},
			gatewayVolumes: []string{
				certsVolume + ":/shared/certs",
				fmt.Sprintf("./%s/config.yaml:/etc/gateway/config.yaml:ro", relBuildDir),
			},
			sidecarPrefix: agentName,
			buildDir:      composeDir,
			exposeGateway: false,
		})

		// Merge pair results into the fleet compose file.
		maps.Copy(compose.Services, pair.services)
		maps.Copy(compose.Volumes, pair.volumes)
	}

	data, err := yaml.Marshal(compose)
	if err != nil {
		return "", fmt.Errorf("marshal fleet compose: %w", err)
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

// mergeCapabilities deduplicates contributed capabilities into the base set.
// Returns base unmodified if contributed is empty.
func mergeCapabilities(base, contributed []string) []string {
	if len(contributed) == 0 {
		return base
	}
	seen := make(map[string]bool, len(base))
	for _, c := range base {
		seen[c] = true
	}
	for _, c := range contributed {
		if !seen[c] {
			base = append(base, c)
			seen[c] = true
		}
	}
	return base
}
