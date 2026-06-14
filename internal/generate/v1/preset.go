package v1

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Preset holds the parsed content of a runtime.yaml file.
type Preset struct {
	Name      string   `yaml:"name"`
	BaseImage string   `yaml:"base_image"`
	Install   []string `yaml:"install"`
	CMD       []string `yaml:"cmd"`
}

// LoadPresets reads all presets from a core directory's presets/ folder.
// Returns a map keyed by "@builtin/<name>".
// Returns an empty map (no error) if the presets directory doesn't exist
// (backward compat with older core versions that don't ship presets).
func LoadPresets(coreDir string) (map[string]*Preset, error) {
	presetsDir := filepath.Join(coreDir, "presets")
	entries, err := os.ReadDir(presetsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read presets dir: %w", err)
	}

	presets := make(map[string]*Preset)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runtimePath := filepath.Join(presetsDir, entry.Name(), "runtime.yaml")
		data, err := os.ReadFile(runtimePath)
		if err != nil {
			continue // skip directories without runtime.yaml
		}
		var p Preset
		if err := yaml.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("parse %s: %w", runtimePath, err)
		}
		key := "@builtin/" + p.Name
		if p.Name == "" {
			key = "@builtin/" + entry.Name()
		}

		// Prepend standard system packages (iptables, gosu, etc.) to install commands.
		// runtime.yaml only declares agent-specific installs.
		p.Install = prependSystemPackages(p.Install)

		presets[key] = &p
	}

	return presets, nil
}

// prependSystemPackages ensures the first install command includes the base system
// packages needed for the sandbox entrypoint (iptables for DNAT, gosu for user switching).
func prependSystemPackages(installs []string) []string {
	if len(installs) == 0 {
		return []string{
			"apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates iptables iputils-ping gosu && rm -rf /var/lib/apt/lists/*",
		}
	}
	// The first install command in runtime.yaml handles apt — augment it with sandbox deps.
	first := installs[0]
	// If it already has iptables, don't modify.
	if contains(first, "iptables") {
		return installs
	}
	// Inject sandbox deps into the existing apt-get install line.
	augmented := augmentAptInstall(first)
	result := make([]string, len(installs))
	result[0] = augmented
	copy(result[1:], installs[1:])
	return result
}

func augmentAptInstall(cmd string) string {
	// Insert " iptables iputils-ping gosu" before "&& rm -rf"
	const marker = "&& rm -rf"
	idx := len(cmd)
	for i := range cmd {
		if i+len(marker) <= len(cmd) && cmd[i:i+len(marker)] == marker {
			idx = i
			break
		}
	}
	return cmd[:idx] + "iptables iputils-ping gosu " + cmd[idx:]
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
