package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ProxyConfig holds the Docker proxy configuration.
type ProxyConfig struct {
	SandboxID           string
	AgentName           string
	NetworkName         string
	AllowedImages       []string
	MaxContainers       int
	MemoryBytes         int64
	NanoCPUs            int64
	PidsLimit           int64
	AllowCompose        bool
	AllowBuild          bool
	AllowedCapabilities []string
	GatewayIP           string // extracted from gateway-route.sh at startup
	GatewayRouteScript  string // cached content of /shared/certs/gateway-route.sh
}

const gatewayRouteScriptPath = "/shared/certs/gateway-route.sh"

// loadGatewayRouteScript reads the gateway-authored routing script and extracts
// the GATEWAY_IP from it. Called once at startup; the content is cached in ProxyConfig.
func loadGatewayRouteScript(cfg *ProxyConfig) error {
	data, err := os.ReadFile(gatewayRouteScriptPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", gatewayRouteScriptPath, err)
	}
	cfg.GatewayRouteScript = string(data)

	// Extract GATEWAY_IP from the script (line like: GATEWAY_IP="172.18.0.2")
	for _, line := range strings.Split(cfg.GatewayRouteScript, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "GATEWAY_IP=") {
			val := strings.TrimPrefix(line, "GATEWAY_IP=")
			val = strings.Trim(val, `"'`)
			cfg.GatewayIP = val
			break
		}
	}
	if cfg.GatewayIP == "" {
		return fmt.Errorf("GATEWAY_IP not found in %s", gatewayRouteScriptPath)
	}
	return nil
}

func loadConfigFromEnv() (*ProxyConfig, error) {
	cfg := &ProxyConfig{}

	cfg.SandboxID = os.Getenv("SANDBOX_ID")
	if cfg.SandboxID == "" {
		return nil, fmt.Errorf("SANDBOX_ID is required")
	}

	cfg.AgentName = os.Getenv("AGENT_NAME")
	if cfg.AgentName == "" {
		return nil, fmt.Errorf("AGENT_NAME is required")
	}

	cfg.NetworkName = os.Getenv("SANDBOX_NETWORK")
	if cfg.NetworkName == "" {
		return nil, fmt.Errorf("SANDBOX_NETWORK is required")
	}

	imagesJSON := os.Getenv("ALLOWED_IMAGES")
	if imagesJSON == "" {
		return nil, fmt.Errorf("ALLOWED_IMAGES is required")
	}
	if err := json.Unmarshal([]byte(imagesJSON), &cfg.AllowedImages); err != nil {
		return nil, fmt.Errorf("parse ALLOWED_IMAGES: %w", err)
	}

	cfg.MaxContainers = envInt("MAX_CONTAINERS", 5)
	cfg.MemoryBytes = parseMemory(os.Getenv("MEMORY_LIMIT"))
	cfg.NanoCPUs = parseCPUs(os.Getenv("CPU_LIMIT"))
	cfg.PidsLimit = int64(envInt("PID_LIMIT", 256))
	cfg.AllowCompose = os.Getenv("ALLOW_COMPOSE") == "true"
	cfg.AllowBuild = os.Getenv("ALLOW_BUILD") == "true"

	capsJSON := os.Getenv("ALLOWED_CAPABILITIES")
	if capsJSON != "" && capsJSON != "null" && capsJSON != "[]" {
		if err := json.Unmarshal([]byte(capsJSON), &cfg.AllowedCapabilities); err != nil {
			return nil, fmt.Errorf("parse ALLOWED_CAPABILITIES: %w", err)
		}
	}
	return cfg, nil
}

func envInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

// parseMemory converts "2g", "512m" to bytes.
func parseMemory(s string) int64 {
	if s == "" {
		return 2 * 1024 * 1024 * 1024 // default 2GB
	}
	s = strings.TrimSpace(strings.ToLower(s))
	multiplier := int64(1)
	if strings.HasSuffix(s, "g") {
		multiplier = 1024 * 1024 * 1024
		s = s[:len(s)-1]
	} else if strings.HasSuffix(s, "m") {
		multiplier = 1024 * 1024
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 2 * 1024 * 1024 * 1024
	}
	return n * multiplier
}

// parseCPUs converts "2" to NanoCPUs (2000000000).
func parseCPUs(s string) int64 {
	if s == "" {
		return 2000000000 // default 2 CPUs
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 2000000000
	}
	return int64(f * 1e9)
}
