// Package ssh implements the SSH feature plugin.
// It provides an SSH server inside the agent container for remote development access.
package ssh

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/donbader/agent-sandbox/internal/resolve"
)

const defaultPort = 2222
const defaultHostKeyPath = ".ssh_host_key"

// Config defines the typed configuration for the ssh plugin.
type Config struct {
	Port           int    `yaml:"port" schema:"SSH port inside the container" default:"2222" examples:"2222,22"`
	AuthorizedKeys string `yaml:"authorized_keys" schema:"Path to public key file (relative to agent.yaml dir)" required:"true" examples:"./ssh_key.pub"`
	HostKey        string `yaml:"host_key" schema:"Path to persistent host private key (auto-generated if absent)" default:".ssh_host_key"`
}

func generateHostKey(path string) error {
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", path, "-N", "", "-C", "")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func init() {
	resolve.Register("ssh", func(projectDir string, cfg Config) (*resolve.FeatureContributions, error) {
		if cfg.AuthorizedKeys == "" {
			return nil, fmt.Errorf("ssh: missing required option 'authorized_keys'")
		}

		port := cfg.Port
		if port == 0 {
			port = defaultPort
		}
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("ssh: port must be between 1 and 65535, got %d", port)
		}

		// Default host_key path if not specified
		if cfg.HostKey == "" {
			cfg.HostKey = defaultHostKeyPath
		}

		// Validate the authorized_keys file exists at generate time.
		keyPath := cfg.AuthorizedKeys
		if !filepath.IsAbs(keyPath) {
			keyPath = filepath.Join(projectDir, keyPath)
		}
		absKeyPath, err := filepath.Abs(keyPath)
		if err != nil {
			return nil, fmt.Errorf("ssh: resolving path %q: %w", cfg.AuthorizedKeys, err)
		}
		absProject, err := filepath.Abs(projectDir)
		if err != nil {
			return nil, fmt.Errorf("ssh: resolving project dir: %w", err)
		}
		if !strings.HasPrefix(absKeyPath, absProject+string(filepath.Separator)) && absKeyPath != absProject {
			return nil, fmt.Errorf("ssh: path %q escapes project directory", cfg.AuthorizedKeys)
		}
		if _, err := os.Stat(keyPath); err != nil {
			return nil, fmt.Errorf("ssh: reading authorized_keys file %q: %w", cfg.AuthorizedKeys, err)
		}

		portStr := strconv.Itoa(port)

		scriptsDir := filepath.Join(projectDir, "scripts")
		if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
			return nil, fmt.Errorf("ssh: creating scripts directory: %w", err)
		}

		// Resolve and auto-generate host key if absent.
		hostKeyPath := cfg.HostKey
		if !filepath.IsAbs(hostKeyPath) {
			hostKeyPath = filepath.Join(projectDir, hostKeyPath)
		}
		absHostKeyPath, err := filepath.Abs(hostKeyPath)
		if err != nil {
			return nil, fmt.Errorf("ssh: resolving path %q: %w", cfg.HostKey, err)
		}
		if !strings.HasPrefix(absHostKeyPath, absProject+string(filepath.Separator)) && absHostKeyPath != absProject {
			return nil, fmt.Errorf("ssh: path %q escapes project directory", cfg.HostKey)
		}
		if _, err := os.Stat(hostKeyPath); err != nil {
			if os.IsNotExist(err) {
				if err := generateHostKey(hostKeyPath); err != nil {
					return nil, fmt.Errorf("ssh: generating host key at %q: %w", cfg.HostKey, err)
				}
			} else {
				return nil, fmt.Errorf("ssh: checking host key at %q: %w", cfg.HostKey, err)
			}
		}

		// The root hook script copies mounted key files into place.
		// Keys are bind-mounted at /run/ssh/ from the host.
		rootHook := fmt.Sprintf(`#!/bin/bash
set -e
cp /run/ssh/host_key /etc/ssh/ssh_host_ed25519_key
chmod 600 /etc/ssh/ssh_host_ed25519_key
ssh-keygen -y -f /etc/ssh/ssh_host_ed25519_key > /etc/ssh/ssh_host_ed25519_key.pub
mkdir -p /home/agent/.ssh
cp /run/ssh/authorized_keys /home/agent/.ssh/authorized_keys
chown -R agent:agent /home/agent/.ssh
/usr/sbin/sshd -p %s
`, portStr)

		rootHookPath := filepath.Join(scriptsDir, "ssh-root-setup.sh")
		if err := os.WriteFile(rootHookPath, []byte(rootHook), 0o755); err != nil {
			return nil, fmt.Errorf("ssh: writing root hook script: %w", err)
		}

		permsHook := `#!/bin/bash
set -e
chmod 700 /home/agent/.ssh
chmod 600 /home/agent/.ssh/authorized_keys
`
		permsHookPath := filepath.Join(scriptsDir, "ssh-perms.sh")
		if err := os.WriteFile(permsHookPath, []byte(permsHook), 0o755); err != nil {
			return nil, fmt.Errorf("ssh: writing entrypoint hook script: %w", err)
		}

		portMapping := fmt.Sprintf("%s:%s", portStr, portStr)

		// Volume mounts: compose file lives in .build/, keys are in project root.
		// Use relative path from .build/ back to project root.
		hostKeyVolume := fmt.Sprintf("../%s:/run/ssh/host_key:ro", cfg.HostKey)
		authKeysVolume := fmt.Sprintf("../%s:/run/ssh/authorized_keys:ro", cfg.AuthorizedKeys)

		return &resolve.FeatureContributions{
			Name: "ssh",
			Commands: []string{
				"apt-get update && apt-get install -y --no-install-recommends openssh-server && rm -rf /var/lib/apt/lists/*",
				"mkdir -p /run/sshd",
				fmt.Sprintf("sed -i 's/^#*Port.*/Port %s/' /etc/ssh/sshd_config", portStr),
				"sed -i 's/^#*PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config",
			},
			RootHooks:       []string{"scripts/ssh-root-setup.sh"},
			EntrypointHooks: []string{"scripts/ssh-perms.sh"},
			Volumes:         []string{hostKeyVolume, authKeysVolume},
			Capabilities:    []string{"SYS_CHROOT"},
			Ports:           []string{portMapping},
		}, nil
	})
}
