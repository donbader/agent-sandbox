package config_test

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalYAML returns a valid base config YAML with the given gateway block appended.
func minimalWithGateway(gatewayYAML string) string {
	return `
name: test-agent
core_version: latest
runtime:
  image: "@builtin/pi"
` + gatewayYAML
}

func TestVPNProfiles_ValidOpenvpn(t *testing.T) {
	yaml := minimalWithGateway(`
gateway:
  vpn_profiles:
    corp:
      type: openvpn
      config_b64: dGVzdA==
  egress:
    - hosts: ["agw.internal.example.com"]
      vpn: corp
      headers:
        Authorization: "Bearer ${TOKEN}"
    - hosts: ["*"]
`)
	cfg, err := config.ParseConfigStrict([]byte(yaml))
	require.NoError(t, err)
	assert.NoError(t, cfg.Validate())
	assert.Equal(t, "openvpn", cfg.Gateway.VPNProfiles["corp"].Type)
	assert.Equal(t, "dGVzdA==", cfg.Gateway.VPNProfiles["corp"].ConfigB64)
}

func TestVPNProfiles_ValidSocks5(t *testing.T) {
	yaml := minimalWithGateway(`
gateway:
  vpn_profiles:
    proxy:
      type: socks5
      address: "vpn-proxy:1080"
  egress:
    - hosts: ["internal.example.com"]
      vpn: proxy
    - hosts: ["*"]
`)
	cfg, err := config.ParseConfigStrict([]byte(yaml))
	require.NoError(t, err)
	assert.NoError(t, cfg.Validate())
	assert.Equal(t, "socks5", cfg.Gateway.VPNProfiles["proxy"].Type)
	assert.Equal(t, "vpn-proxy:1080", cfg.Gateway.VPNProfiles["proxy"].Address)
}

func TestVPNProfiles_MissingType(t *testing.T) {
	yaml := minimalWithGateway(`
gateway:
  vpn_profiles:
    corp:
      config_b64: dGVzdA==
  egress:
    - hosts: ["*"]
`)
	cfg, err := config.ParseConfigStrict([]byte(yaml))
	require.NoError(t, err)
	err = cfg.Validate()
	assert.ErrorContains(t, err, `vpn_profiles["corp"]: type is required`)
}

func TestVPNProfiles_UnsupportedType(t *testing.T) {
	yaml := minimalWithGateway(`
gateway:
  vpn_profiles:
    corp:
      type: wireguard
      config_b64: dGVzdA==
  egress:
    - hosts: ["*"]
`)
	cfg, err := config.ParseConfigStrict([]byte(yaml))
	require.NoError(t, err)
	err = cfg.Validate()
	assert.ErrorContains(t, err, `unsupported type "wireguard"`)
}

func TestVPNProfiles_OpenvpnMissingConfigB64(t *testing.T) {
	yaml := minimalWithGateway(`
gateway:
  vpn_profiles:
    corp:
      type: openvpn
  egress:
    - hosts: ["*"]
`)
	cfg, err := config.ParseConfigStrict([]byte(yaml))
	require.NoError(t, err)
	err = cfg.Validate()
	assert.ErrorContains(t, err, `vpn_profiles["corp"]: config_b64 is required for type 'openvpn'`)
}

func TestVPNProfiles_Socks5MissingAddress(t *testing.T) {
	yaml := minimalWithGateway(`
gateway:
  vpn_profiles:
    proxy:
      type: socks5
  egress:
    - hosts: ["*"]
`)
	cfg, err := config.ParseConfigStrict([]byte(yaml))
	require.NoError(t, err)
	err = cfg.Validate()
	assert.ErrorContains(t, err, `vpn_profiles["proxy"]: address is required for type 'socks5'`)
}

func TestVPNProfiles_CrossRef_UndefinedProfile(t *testing.T) {
	yaml := minimalWithGateway(`
gateway:
  vpn_profiles:
    corp:
      type: socks5
      address: "proxy:1080"
  egress:
    - hosts: ["internal.example.com"]
      vpn: nonexistent
    - hosts: ["*"]
`)
	cfg, err := config.ParseConfigStrict([]byte(yaml))
	require.NoError(t, err)
	err = cfg.Validate()
	assert.ErrorContains(t, err, `vpn profile "nonexistent" is not defined in gateway.vpn_profiles`)
}

func TestVPNProfiles_CrossRef_ValidRef(t *testing.T) {
	yaml := minimalWithGateway(`
gateway:
  vpn_profiles:
    corp:
      type: socks5
      address: "proxy:1080"
  egress:
    - hosts: ["internal.example.com"]
      vpn: corp
    - hosts: ["*"]
`)
	cfg, err := config.ParseConfigStrict([]byte(yaml))
	require.NoError(t, err)
	assert.NoError(t, cfg.Validate())
}

func TestVPNProfiles_EgressDenyAndVPN_Invalid(t *testing.T) {
	yaml := minimalWithGateway(`
gateway:
  vpn_profiles:
    corp:
      type: socks5
      address: "proxy:1080"
  egress:
    - hosts: ["blocked.example.com"]
      deny: true
      vpn: corp
    - hosts: ["*"]
`)
	cfg, err := config.ParseConfigStrict([]byte(yaml))
	require.NoError(t, err)
	err = cfg.Validate()
	assert.ErrorContains(t, err, "cannot have both deny: true and vpn")
}

func TestVPNProfiles_NoProfiles_Valid(t *testing.T) {
	yaml := minimalWithGateway(`
gateway:
  egress:
    - hosts: ["*"]
`)
	cfg, err := config.ParseConfigStrict([]byte(yaml))
	require.NoError(t, err)
	assert.NoError(t, cfg.Validate())
	assert.Empty(t, cfg.Gateway.VPNProfiles)
}
