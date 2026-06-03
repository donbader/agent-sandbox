package ssh

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSHPlugin_DefaultPort(t *testing.T) {
	projectDir := t.TempDir()
	pubkeyFile := filepath.Join(projectDir, "id_ed25519.pub")
	require.NoError(t, os.WriteFile(pubkeyFile, []byte("ssh-ed25519 AAAAC3Nz testuser@host\n"), 0o644))

	plugin := resolve.RegisteredPlugins()["ssh"]
	require.NotNil(t, plugin, "ssh plugin not registered")

	contrib, err := plugin.Resolve(projectDir, map[string]any{
		"authorized_keys": "id_ed25519.pub",
	})
	require.NoError(t, err)

	assert.Equal(t, "ssh", contrib.Name)
	assert.Equal(t, []string{"2222:2222"}, contrib.Ports)
	assert.Equal(t, []string{"SYS_CHROOT"}, contrib.Capabilities)
	assert.Equal(t, []string{"scripts/ssh-root-setup.sh"}, contrib.RootHooks)
	assert.Equal(t, []string{"scripts/ssh-perms.sh"}, contrib.EntrypointHooks)

	require.Len(t, contrib.Commands, 4)
	assert.Contains(t, contrib.Commands[0], "openssh-server")
	assert.Contains(t, contrib.Commands[2], "Port 2222")
	assert.Contains(t, contrib.Commands[3], "PasswordAuthentication no")
}

func TestSSHPlugin_CustomPort(t *testing.T) {
	projectDir := t.TempDir()
	pubkeyFile := filepath.Join(projectDir, "key.pub")
	require.NoError(t, os.WriteFile(pubkeyFile, []byte("ssh-ed25519 AAAAC3Nz testuser@host\n"), 0o644))

	plugin := resolve.RegisteredPlugins()["ssh"]
	require.NotNil(t, plugin, "ssh plugin not registered")

	contrib, err := plugin.Resolve(projectDir, map[string]any{
		"authorized_keys": "key.pub",
		"port":            8022,
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"8022:8022"}, contrib.Ports)
	assert.Contains(t, contrib.Commands[2], "Port 8022")
}

func TestSSHPlugin_WritesRootHookScript(t *testing.T) {
	projectDir := t.TempDir()
	pubkeyFile := filepath.Join(projectDir, "id_rsa.pub")
	pubkey := "ssh-rsa AAAAB3NzaC1yc2EAAA testuser@host"
	require.NoError(t, os.WriteFile(pubkeyFile, []byte(pubkey+"\n"), 0o644))

	plugin := resolve.RegisteredPlugins()["ssh"]
	require.NotNil(t, plugin, "ssh plugin not registered")

	_, err := plugin.Resolve(projectDir, map[string]any{
		"authorized_keys": "id_rsa.pub",
	})
	require.NoError(t, err)

	rootHookPath := filepath.Join(projectDir, "scripts", "ssh-root-setup.sh")
	content, err := os.ReadFile(rootHookPath)
	require.NoError(t, err)

	assert.Contains(t, string(content), "ssh_host_ed25519_key")
	assert.Contains(t, string(content), "/usr/sbin/sshd -p 2222")
	assert.Contains(t, string(content), pubkey)
	assert.Contains(t, string(content), "chown -R agent:agent /home/agent/.ssh")
}

func TestSSHPlugin_WritesPermsHookScript(t *testing.T) {
	projectDir := t.TempDir()
	pubkeyFile := filepath.Join(projectDir, "key.pub")
	require.NoError(t, os.WriteFile(pubkeyFile, []byte("ssh-ed25519 AAAAC3Nz testuser@host\n"), 0o644))

	plugin := resolve.RegisteredPlugins()["ssh"]
	require.NotNil(t, plugin, "ssh plugin not registered")

	_, err := plugin.Resolve(projectDir, map[string]any{
		"authorized_keys": "key.pub",
	})
	require.NoError(t, err)

	permsHookPath := filepath.Join(projectDir, "scripts", "ssh-perms.sh")
	content, err := os.ReadFile(permsHookPath)
	require.NoError(t, err)

	assert.Contains(t, string(content), "chmod 700 /home/agent/.ssh")
	assert.Contains(t, string(content), "chmod 600 /home/agent/.ssh/authorized_keys")
}

func TestSSHPlugin_ErrorsWithoutAuthorizedKeys(t *testing.T) {
	plugin := resolve.RegisteredPlugins()["ssh"]
	require.NotNil(t, plugin, "ssh plugin not registered")

	_, err := plugin.Resolve(t.TempDir(), map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required option 'authorized_keys'")
}

func TestSSHPlugin_ErrorsWhenKeyFileNotFound(t *testing.T) {
	plugin := resolve.RegisteredPlugins()["ssh"]
	require.NotNil(t, plugin, "ssh plugin not registered")

	_, err := plugin.Resolve(t.TempDir(), map[string]any{
		"authorized_keys": "nonexistent.pub",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading authorized_keys file")
}
