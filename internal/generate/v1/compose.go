package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/envvar"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/donbader/agent-sandbox/internal/runtime"
	"gopkg.in/yaml.v3"
)


type composeFile struct {
	Services map[string]any `yaml:"services"`
	Volumes  map[string]any `yaml:"volumes,omitempty"`
	Networks map[string]any `yaml:"networks,omitempty"`
}

// agentPairParams holds the varying values between single-agent and fleet compose generation.
type agentPairParams struct {
	cfg              *config.Config
	contribs         *plugin.Contributions
	agentName        string
	gatewayName      string
	agentAlias       string
	gatewayAlias     string
	certsVolume      string
	agentBuild       map[string]any
	gatewayBuild     map[string]any
	gatewayVolumes   []string
	sidecarPrefix    string
	buildDir         string
	exposeGateway    bool
	projectName      string
	gatewaySandboxIP string
	sandboxCIDR      string
}

// agentPairResult holds the services and volumes produced by buildAgentPair.
type agentPairResult struct {
	services map[string]any
	volumes  map[string]any
	networks map[string]any // extra networks from egress rules
}

// buildAgentPair constructs the agent service, gateway service, and sidecar services
// for a single agent-gateway pair. Both BuildCompose and BuildFleetCompose delegate here.
func buildAgentPair(p agentPairParams) (agentPairResult, error) {
	result := agentPairResult{
		services: map[string]any{},
		volumes:  map[string]any{},
		networks: map[string]any{},
	}

	cfg := p.cfg
	contribs := p.contribs

	// Agent volumes: certs + namespaced volumes (auto-prefixed) + raw volumes (as-is).
	agentVolumes := []string{p.certsVolume + ":/shared/certs"}
	agentVolumes = append(agentVolumes, namespaceVolumes(p.agentName, cfg.Runtime.NamespacedVolumes)...)
	agentVolumes = append(agentVolumes, cfg.Runtime.RawVolumes...)
	if contribs != nil {
		agentVolumes = append(agentVolumes, namespaceVolumes(p.agentName, contribs.Runtime.NamespacedVolumes)...)
		agentVolumes = append(agentVolumes, contribs.Runtime.RawVolumes...)
	}
	if err := validateVolumes(agentVolumes); err != nil {
		return agentPairResult{}, fmt.Errorf("agent %q: %w", p.agentName, err)
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
			"GATEWAY_HOST":        p.gatewayAlias,
			"NODE_EXTRA_CA_CERTS": "/shared/certs/ca.crt",
			"NODE_USE_SYSTEM_CA":  "1",
			"SSL_CERT_FILE":       "/etc/ssl/certs/ca-certificates.crt",
		},
	}

	// Merge plugin-contributed runtime.environment into agent service env.
	// Merged before user-defined env so user config takes precedence.
	if contribs != nil && len(contribs.Runtime.Environment) > 0 {
		if envMap, ok := agentSvc["environment"].(map[string]string); ok {
			maps.Copy(envMap, contribs.Runtime.Environment)
		}
	}

	// Merge user-defined runtime.environment into agent service env.
	// User config wins over plugin contributions on conflict.
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
	gatewayVolumes := append([]string{}, p.gatewayVolumes...)
	if contribs != nil {
		gatewayVolumes = append(gatewayVolumes, namespaceVolumes(p.agentName, contribs.Gateway.NamespacedVolumes)...)
		gatewayVolumes = append(gatewayVolumes, contribs.Gateway.RawVolumes...)
	}
	if err := validateVolumes(gatewayVolumes); err != nil {
		return agentPairResult{}, fmt.Errorf("agent %q gateway: %w", p.agentName, err)
	}
	gatewaySvc := map[string]any{
		"build":    p.gatewayBuild,
		"cap_drop": []string{"ALL"},
		"cap_add":  []string{"NET_ADMIN", "NET_BIND_SERVICE"},
		"networks": map[string]any{
			"sandbox": map[string]any{
				"aliases":      []string{p.gatewayAlias},
				"ipv4_address": p.gatewaySandboxIP,
			},
			"external": map[string]any{},
		},
		"volumes": gatewayVolumes,
		"healthcheck": map[string]any{
			"test":     []string{"CMD", "wget", "--spider", "-q", "http://localhost:8080/health"},
			"interval": "5s",
			"timeout":  "3s",
			"retries":  3,
		},
	}

	// Expose gateway HTTP port for port discovery via `docker compose port`.
	if p.exposeGateway {
		gatewaySvc["ports"] = []string{"8080"}
	}
	// Wire log_level from agent config to gateway container.
	if cfg.LogLevel != "" {
		gatewayEnv = append(gatewayEnv, "LOG_LEVEL="+cfg.LogLevel)
	}
	// Provide the gateway's sandbox IP and CIDR so it can write a reliable routing
	// script and configure DNS local-network filtering.
	gatewayEnv = append(gatewayEnv, "GATEWAY_SANDBOX_IP="+p.gatewaySandboxIP)
	gatewayEnv = append(gatewayEnv, "GATEWAY_SANDBOX_CIDR="+p.sandboxCIDR)
	if len(gatewayEnv) > 0 {
		gatewaySvc["environment"] = gatewayEnv
	}
	// Attach extra networks from egress rules to the gateway service.
	// These are pre-existing Docker networks (external: true) that the gateway
	// must join to reach services on those networks.
	if gwNets, ok := gatewaySvc["networks"].(map[string]any); ok {
		for _, rule := range cfg.Gateway.Egress {
			if rule.Network != "" {
				gwNets[rule.Network] = map[string]any{}
				result.networks[rule.Network] = map[string]any{
					"external": true,
				}
			}
		}
		// Also check legacy services for backward compat
		for _, svc := range cfg.Gateway.Services {
			if svc.Network != "" {
				gwNets[svc.Network] = map[string]any{}
				result.networks[svc.Network] = map[string]any{
					"external": true,
				}
			}
		}
	}
	result.services[p.gatewayName] = gatewaySvc

	// Sidecar services from plugins
	if contribs != nil {
		for name, svc := range contribs.Sidecar.Services {
			sidecar := buildSidecarService(svc, p.buildDir)
			// Inject system env vars into all sidecars.
			injectSidecarSystemEnv(sidecar, p.cfg.Name, p.projectName)
			// Namespace named volumes (e.g. "buildkit-data:/path" → "dorey-002-buildkit-data:/path").
			// Must happen BEFORE injectSidecarGatewayRouting which adds already-namespaced certs volume.
			if vols, ok := sidecar["volumes"].([]string); ok {
				sidecar["volumes"] = namespaceVolumes(p.agentName, vols)
			}
			// Inject gateway routing infrastructure (cap_add, certs volume, GATEWAY_HOST).
			injectSidecarGatewayRouting(sidecar, p.agentName, p.certsVolume)
			// Healthcheck: verify sidecar can reach gateway (DNS + routing).
			injectSidecarHealthcheck(sidecar, p.agentName)
			// Config fingerprint MUST be last — hash the final service state.
			injectConfigFingerprint(sidecar)
			// Sidecars implicitly depend on the agent service being started.
			if sidecar["depends_on"] == nil {
				sidecar["depends_on"] = map[string]any{
					p.agentName: map[string]any{
						"condition": "service_started",
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

	// Extract named volumes from user config (namespaced)
	for _, v := range cfg.Runtime.NamespacedVolumes {
		if volName := extractVolumeName(v); volName != "" {
			result.volumes[p.agentName+"-"+volName] = nil
		}
	}
	// Extract named volumes from user config (raw)
	for _, v := range cfg.Runtime.RawVolumes {
		if volName := extractVolumeName(v); volName != "" {
			result.volumes[volName] = nil
		}
	}

	// Extract named volumes from plugin contributions
	if contribs != nil {
		for _, v := range contribs.Runtime.NamespacedVolumes {
			if volName := extractVolumeName(v); volName != "" {
				result.volumes[p.agentName+"-"+volName] = nil
			}
		}
		for _, v := range contribs.Runtime.RawVolumes {
			if volName := extractVolumeName(v); volName != "" {
				result.volumes[volName] = nil
			}
		}
		for _, v := range contribs.Gateway.NamespacedVolumes {
			if volName := extractVolumeName(v); volName != "" {
				result.volumes[p.agentName+"-"+volName] = nil
			}
		}
		for _, v := range contribs.Gateway.RawVolumes {
			if volName := extractVolumeName(v); volName != "" {
				result.volumes[volName] = nil
			}
		}
		// Extract named volumes from sidecar services.
		for _, svc := range contribs.Sidecar.Services {
			for _, v := range svc.Volumes {
				if volName := extractVolumeName(v); volName != "" {
					result.volumes[p.agentName+"-"+volName] = nil
				}
			}
		}
	}

	return result, nil
}

// ComposeAgentEntry holds the data needed to generate one agent's services in a fleet compose file.
type ComposeAgentEntry struct {
	Config   *config.Config
	Contribs *plugin.Contributions
	BuildDir string // absolute path to the agent's .build/<name>/ directory
}

// BuildProjectCompose generates a unified docker-compose.yml for any project (1 or N agents).
// Gateway port is always exposed for port discovery via `docker compose port`.
func BuildProjectCompose(agents []ComposeAgentEntry, projectDir string) (string, error) {
	subnet := findAvailableSubnet()

	compose := composeFile{
		Services: map[string]any{},
		Volumes:  map[string]any{},
		Networks: map[string]any{
			"sandbox": map[string]any{
				"driver":   "bridge",
				"internal": true,
				"ipam": map[string]any{
					"config": []map[string]any{
						{"subnet": subnet.CIDR},
					},
				},
			},
			"external": map[string]any{
				"driver": "bridge",
			},
		},
	}

	for i, agent := range agents {
		cfg := agent.Config
		agentName := cfg.Name
		gatewayName := cfg.Name + "-gateway"
		certsVolume := agentName + "-certs"
		// Each gateway gets a unique static IP on the sandbox subnet (.2, .3, .4, ...).
		gatewaySandboxIP := fmt.Sprintf("%s.%d", subnet.Prefix, i+2)

		relBuildDir, err := filepath.Rel(filepath.Join(projectDir, ".build"), agent.BuildDir)
		if err != nil {
			relBuildDir = agent.BuildDir
		}

		composeDir := filepath.Join(projectDir, ".build")

		pair, err := buildAgentPair(agentPairParams{
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
				"context":    fmt.Sprintf("./%s/gateway", relBuildDir),
				"dockerfile": "Dockerfile",
			},
			gatewayVolumes: []string{
				certsVolume + ":/shared/certs",
			},
			sidecarPrefix:    agentName,
			buildDir:         composeDir,
			projectName:      filepath.Base(projectDir),
			exposeGateway:    false,
			gatewaySandboxIP: gatewaySandboxIP,
			sandboxCIDR:      subnet.CIDR,
		})
		if err != nil {
			return "", err
		}

		maps.Copy(compose.Services, pair.services)
		maps.Copy(compose.Volumes, pair.volumes)
		maps.Copy(compose.Networks, pair.networks)
	}

	// Validate network isolation: non-gateway services must only be on internal networks.
	if err := validateNetworkIsolation(compose.Services, compose.Networks); err != nil {
		return "", err
	}

	data, err := yaml.Marshal(compose)
	if err != nil {
		return "", fmt.Errorf("marshal compose: %w", err)
	}
	return string(data), nil
}

// validateNetworkIsolation checks that non-gateway services are only on internal networks.
// This is a defense-in-depth check — the generator already assigns networks correctly by
// construction, but this catches regressions or future code changes that might violate
// the security model.
func validateNetworkIsolation(services map[string]any, networks map[string]any) error {
	// Build set of internal networks.
	internalNets := map[string]bool{}
	for name, cfg := range networks {
		if m, ok := cfg.(map[string]any); ok {
			if internal, _ := m["internal"].(bool); internal {
				internalNets[name] = true
			}
		}
	}

	for svcName, svcDef := range services {
		// Gateway services are allowed on any network.
		if strings.Contains(svcName, "-gateway") {
			continue
		}

		svc, ok := svcDef.(map[string]any)
		if !ok {
			continue
		}

		// Extract network names from the service definition.
		var netNames []string
		switch nets := svc["networks"].(type) {
		case []string:
			netNames = nets
		case []any:
			for _, n := range nets {
				if s, ok := n.(string); ok {
					netNames = append(netNames, s)
				}
			}
		case map[string]any:
			for name := range nets {
				netNames = append(netNames, name)
			}
		}

		// Verify all networks are internal.
		for _, net := range netNames {
			if !internalNets[net] {
				return fmt.Errorf("service %q is on non-internal network %q — only gateway services may use external networks", svcName, net)
			}
		}
	}

	return nil
}

// buildSidecarService constructs the compose service definition for a plugin sidecar.
func buildSidecarService(svc plugin.ComposeService, buildDir string) map[string]any {
	s := map[string]any{
		"networks": []string{"sandbox"},
	}
	if svc.Build != "" {
		buildPath := svc.Build
		// For bundled plugins, the build path is relative to projectDir.
		// Make it absolute so filepath.Rel works correctly.
		if !filepath.IsAbs(buildPath) {
			// buildDir is projectDir/.build — go up one level to get projectDir
			buildPath = filepath.Join(filepath.Dir(buildDir), buildPath)
		}
		relPath, err := filepath.Rel(buildDir, buildPath)
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
	if len(svc.CapAdd) > 0 {
		s["cap_add"] = svc.CapAdd
	}
	if len(svc.SecurityOpt) > 0 {
		s["security_opt"] = svc.SecurityOpt
	}
	if len(svc.Tmpfs) > 0 {
		s["tmpfs"] = svc.Tmpfs
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

// namespaceVolume prefixes the named volume portion of a volume mount string with
// the agent name to ensure per-agent isolation. Bind mounts (starting with . or /)
// are returned unchanged.
// Example: namespaceVolume("dorey-001", "oauth-tokens:/data") → "dorey-001-oauth-tokens:/data"
func namespaceVolume(agentName, volume string) string {
	for i, c := range volume {
		if c == ':' {
			name := volume[:i]
			if len(name) > 0 && name[0] != '.' && name[0] != '/' {
				return agentName + "-" + volume
			}
			return volume
		}
	}
	return volume
}

// namespaceVolumes applies namespaceVolume to each entry in a slice.
func namespaceVolumes(agentName string, volumes []string) []string {
	if len(volumes) == 0 {
		return volumes
	}
	result := make([]string, len(volumes))
	for i, v := range volumes {
		result[i] = namespaceVolume(agentName, v)
	}
	return result
}

// collectGatewayEnvVars extracts env var names referenced in gateway service headers
// and plugin options, returning them as docker-compose environment entries (passthrough format).
func collectGatewayEnvVars(cfg *config.Config, contribs *plugin.Contributions) []string {
	seen := map[string]bool{}

	// From user gateway config (legacy services)
	for _, svc := range cfg.Gateway.Services {
		for _, value := range svc.Headers {
			if ev := envvar.Extract(value); ev != "" {
				seen[ev] = true
			}
		}
	}

	// From egress rules (new format)
	for _, rule := range cfg.Gateway.Egress {
		for _, value := range rule.Headers {
			if ev := envvar.Extract(value); ev != "" {
				seen[ev] = true
			}
		}
	}

	// From plugin contributions (egress rule headers)
	if contribs != nil {
		for _, rule := range contribs.Gateway.Egress {
			for _, value := range rule.Headers {
				if ev := envvar.Extract(value); ev != "" {
					seen[ev] = true
				}
			}
		}
	}

	// From plugin installation options (${VAR} references resolved at gateway startup)
	for _, inst := range cfg.Installations {
		extractEnvVarsFromOptions(inst.Options, seen)
	}

	var envVars []string
	for v := range seen {
		envVars = append(envVars, v)
	}
	return envVars
}

// extractEnvVarsFromOptions recursively scans plugin options for ${VAR} references.
func extractEnvVarsFromOptions(opts map[string]any, seen map[string]bool) {
	for _, v := range opts {
		scanValueForEnvVars(v, seen)
	}
}

func scanValueForEnvVars(v any, seen map[string]bool) {
	switch val := v.(type) {
	case string:
		if ev := envvar.Extract(val); ev != "" {
			seen[ev] = true
		}
	case map[string]any:
		for _, nested := range val {
			scanValueForEnvVars(nested, seen)
		}
	case []any:
		for _, item := range val {
			scanValueForEnvVars(item, seen)
		}
	}
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

// validateVolumes returns an error if any volume spec is empty.
// Empty specs indicate a bug in a plugin's volume template logic.
// dangerousSocketPaths is derived from the runtime package — single source of truth.
var dangerousSocketPaths = runtime.DangerousSocketPaths()

func validateVolumes(vols []string) error {
	for _, v := range vols {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("invalid empty volume spec (check plugin volume templates for conditional logic that produces empty strings)")
		}
		// Block dangerous host sockets from being mounted into agent containers.
		src := strings.SplitN(v, ":", 2)[0]
		for _, sock := range dangerousSocketPaths {
			if src == sock {
				return fmt.Errorf("mounting %s into the agent container is not allowed (use a policy-enforcing sidecar instead)", sock)
			}
		}
	}
	return nil
}

// injectSidecarSystemEnv adds well-known env vars to a sidecar service.
// These provide the sidecar with sandbox identity and network information.
func injectSidecarSystemEnv(sidecar map[string]any, agentName, projectName string) {
	env, ok := sidecar["environment"].(map[string]string)
	if !ok || env == nil {
		env = make(map[string]string)
	}
	env["SANDBOX_ID"] = projectName + "-" + agentName
	env["SANDBOX_NETWORK"] = projectName + "_sandbox"
	env["AGENT_NAME"] = agentName
	sidecar["environment"] = env
}

// injectConfigFingerprint adds a label with a hash of the entire sidecar service config.
// This ensures docker compose recreates the container when ANY config changes
// (env, cap_add, security_opt, volumes, image, etc.) — zero maintenance.
func injectConfigFingerprint(sidecar map[string]any) {
	// Marshal the full service config (json sorts map keys for determinism)
	raw, _ := json.Marshal(sidecar)
	h := sha256.Sum256(raw)
	labels, _ := sidecar["labels"].(map[string]string)
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["com.agent-sandbox.config-hash"] = hex.EncodeToString(h[:8])
	sidecar["labels"] = labels
}

// injectSidecarGatewayRouting adds gateway routing infrastructure to all sidecar services.
// This allows sidecars to route traffic through the gateway for credential injection and
// outbound network access.
func injectSidecarGatewayRouting(sidecar map[string]any, agentName, certsVolume string) {
	// Add NET_ADMIN capability for iptables.
	capAdd, _ := sidecar["cap_add"].([]string)
	capAdd = append(capAdd, "NET_ADMIN")
	sidecar["cap_add"] = capAdd

	// Add certs volume so sidecar can install the gateway CA.
	volumes, _ := sidecar["volumes"].([]string)
	volumes = append(volumes, certsVolume+":/shared/certs")
	sidecar["volumes"] = volumes

	// Add GATEWAY_HOST and CA trust env vars.
	env, ok := sidecar["environment"].(map[string]string)
	if !ok || env == nil {
		env = make(map[string]string)
	}
	env["GATEWAY_HOST"] = agentName + "-gateway"
	env["NODE_EXTRA_CA_CERTS"] = "/shared/certs/ca.crt"
	env["NODE_USE_SYSTEM_CA"] = "1"
	env["SSL_CERT_FILE"] = "/etc/ssl/certs/ca-certificates.crt"
	sidecar["environment"] = env
}

// injectSidecarHealthcheck adds a healthcheck that verifies the sidecar can reach
// the gateway. Works on Alpine (wget) and Debian (curl) based images.
func injectSidecarHealthcheck(sidecar map[string]any, agentName string) {
	// Don't override user-defined healthchecks.
	if _, ok := sidecar["healthcheck"]; ok {
		return
	}
	gatewayHost := agentName + "-gateway"
	sidecar["healthcheck"] = map[string]any{
		"test":         []string{"CMD-SHELL", "wget -q --spider --timeout=5 http://" + gatewayHost + ":8080/health || curl -sf --max-time 5 http://" + gatewayHost + ":8080/health"},
		"interval":     "30s",
		"timeout":      "10s",
		"retries":      3,
		"start_period": "15s",
	}
}
