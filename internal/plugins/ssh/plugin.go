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

		// Default host_key path if not specified
		if cfg.HostKey == "" {
			cfg.HostKey = defaultHostKeyPath
		}

		// Resolve the authorized_keys file relative to projectDir.
		keyPath := cfg.AuthorizedKeys
		if !filepath.IsAbs(keyPath) {
			keyPath = filepath.Join(projectDir, keyPath)
		}

		pubkeyBytes, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("ssh: reading authorized_keys file %q: %w", cfg.AuthorizedKeys, err)
		}
		pubkey := strings.TrimSpace(string(pubkeyBytes))

		portStr := strconv.Itoa(port)

		scriptsDir := filepath.Join(projectDir, "scripts")
		if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
			return nil, fmt.Errorf("ssh: creating scripts directory: %w", err)
		}

		// Resolve and auto-generate host key
		hostKeyPath := cfg.HostKey
		if !filepath.IsAbs(hostKeyPath) {
			hostKeyPath = filepath.Join(projectDir, hostKeyPath)
		}
		if _, err := os.Stat(hostKeyPath); os.IsNotExist(err) {
			if err := generateHostKey(hostKeyPath); err != nil {
				return nil, fmt.Errorf("ssh: generating host key at %q: %w", cfg.HostKey, err)
			}
		}
		hostKeyBytes, err := os.ReadFile(hostKeyPath)
		if err != nil {
			return nil, fmt.Errorf("ssh: reading host_key file %q: %w", cfg.HostKey, err)
		}
		hostKey := strings.TrimSpace(string(hostKeyBytes))

		hostKeySetup := fmt.Sprintf(`cat > /etc/ssh/ssh_host_ed25519_key << 'HOSTKEY'
%s
HOSTKEY
chmod 600 /etc/ssh/ssh_host_ed25519_key
ssh-keygen -y -f /etc/ssh/ssh_host_ed25519_key > /etc/ssh/ssh_host_ed25519_key.pub`, hostKey)

		rootHook := fmt.Sprintf(`#!/bin/bash
set -e
%s
mkdir -p /home/agent/.ssh
cat > /home/agent/.ssh/authorized_keys << 'PUBKEY'
%s
PUBKEY
chown -R agent:agent /home/agent/.ssh
/usr/sbin/sshd -p %s
`, hostKeySetup, pubkey, portStr)

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
			Capabilities:    []string{"SYS_CHROOT"},
			Ports:           []string{portMapping},
		}, nil
	})
}
