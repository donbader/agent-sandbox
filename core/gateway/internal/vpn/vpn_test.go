package vpn

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIfaceIsUp_NonExistent(t *testing.T) {
	assert.False(t, ifaceIsUp("tunXXXXnotreal"))
}

func TestStartTunnel_BadType(t *testing.T) {
	err := StartTunnel("test", ProfileConfig{Type: "wireguard", ConfigB64: "x"}, "tun99")
	assert.ErrorContains(t, err, "unsupported VPN type")
}

func TestStartTunnel_BadBase64(t *testing.T) {
	err := StartTunnel("test", ProfileConfig{Type: "openvpn", ConfigB64: "!!!notbase64!!!"}, "tun99")
	assert.ErrorContains(t, err, "decode config_b64")
}

func TestNewBoundDialer_ReturnsDialer(t *testing.T) {
	d := NewBoundDialer("tun0", 10e9)
	assert.NotNil(t, d)
}
