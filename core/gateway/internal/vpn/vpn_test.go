package vpn

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestGenerateTOTP_RFC4226_KnownVectors verifies the TOTP algorithm against known
// RFC 4226 test vectors (the same secret and time-steps used in RFC 6238 Appendix B).
//
// Secret (ASCII): "12345678901234567890"
// Base32:         GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ
//
// T=1  (Unix=59)        → 287082
// T=37037036 (Unix=1111111109) → 081804
// T=37037037 (Unix=1111111111) → 050471
func TestGenerateTOTP_RFC4226_KnownVectors(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
	}
	for _, tc := range cases {
		got, err := generateTOTPAtTime(secret, time.Unix(tc.unix, 0))
		require.NoError(t, err, "unix=%d", tc.unix)
		assert.Equal(t, tc.want, got, "unix=%d", tc.unix)
	}
}

func TestGenerateTOTP_InvalidSecret(t *testing.T) {
	_, err := generateTOTPAtTime("!!!notbase32!!!", time.Now())
	assert.ErrorContains(t, err, "totp: decode secret")
}

func TestGenerateTOTP_SixDigits(t *testing.T) {
	// Any valid secret must always produce a 6-character numeric string.
	secret := "JBSWY3DPEHPK3PXP" // "Hello!" in base32
	code, err := generateTOTPAtTime(secret, time.Now())
	require.NoError(t, err)
	assert.Len(t, code, 6, "TOTP code must be exactly 6 digits")
	for _, ch := range code {
		assert.True(t, ch >= '0' && ch <= '9', "character %q is not a digit", ch)
	}
}

func TestGenerateTOTP_PaddingTolerant(t *testing.T) {
	// With and without = padding must produce the same code.
	t1 := time.Unix(1111111109, 0)
	withPad, err := generateTOTPAtTime("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", t1)
	require.NoError(t, err)
	noPad, err := generateTOTPAtTime("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ====", t1)
	require.NoError(t, err)
	assert.Equal(t, withPad, noPad)
}
