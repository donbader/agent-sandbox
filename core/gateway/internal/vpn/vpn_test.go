package vpn

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSortedKeys_Empty(t *testing.T) {
	keys := sortedKeys(map[string]ProfileConfig{})
	assert.Empty(t, keys)
}

func TestSortedKeys_Sorted(t *testing.T) {
	m := map[string]ProfileConfig{
		"zebra": {Type: "openvpn"},
		"alpha": {Type: "openvpn"},
		"mango": {Type: "openvpn"},
	}
	keys := sortedKeys(m)
	assert.Equal(t, []string{"alpha", "mango", "zebra"}, keys)
}

func TestIfaceIsUp_NonExistent(t *testing.T) {
	assert.False(t, ifaceIsUp("tunXXXXnotreal"))
}

func TestNewManager_EmptyProfiles(t *testing.T) {
	m, err := New(map[string]ProfileConfig{})
	assert.NoError(t, err)
	assert.NotNil(t, m)
	assert.Nil(t, m.DialerFor("anything"))
}

func TestNewManager_NilReceiver(t *testing.T) {
	var m *Manager
	assert.Nil(t, m.DialerFor("anything"))
}

func TestStartTunnel_BadType(t *testing.T) {
	err := StartTunnel("test", ProfileConfig{Type: "wireguard", ConfigB64: "x"}, "tun99")
	assert.ErrorContains(t, err, "unsupported VPN type")
}

func TestStartTunnel_BadBase64(t *testing.T) {
	err := StartTunnel("test", ProfileConfig{Type: "openvpn", ConfigB64: "!!!notbase64!!!"}, "tun99")
	assert.ErrorContains(t, err, "decode config_b64")
}
