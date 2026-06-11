package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateAndStore(t *testing.T) {
	tmpDir := t.TempDir()
	sharedCertPath := filepath.Join(tmpDir, "shared", "ca.crt")
	privateKeyPath := filepath.Join(tmpDir, "shared", "ca.key")

	tlsCert, err := GenerateAndStore(sharedCertPath, privateKeyPath)
	require.NoError(t, err)

	// Verify the returned tls.Certificate has a valid cert and key
	require.Len(t, tlsCert.Certificate, 1)
	require.IsType(t, &ecdsa.PrivateKey{}, tlsCert.PrivateKey)

	// Parse and verify the cert is a CA
	cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	require.NoError(t, err)
	assert.True(t, cert.IsCA)
	assert.Equal(t, "agent-sandbox CA", cert.Subject.CommonName)
	assert.Equal(t, []string{"agent-sandbox"}, cert.Subject.Organization)

	// Verify cert file on disk matches
	certPEM, err := os.ReadFile(sharedCertPath)
	require.NoError(t, err)
	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	assert.Equal(t, "CERTIFICATE", block.Type)
	assert.Equal(t, tlsCert.Certificate[0], block.Bytes)

	// Verify key file on disk
	keyPEM, err := os.ReadFile(privateKeyPath)
	require.NoError(t, err)
	keyBlock, _ := pem.Decode(keyPEM)
	require.NotNil(t, keyBlock)
	assert.Equal(t, "EC PRIVATE KEY", keyBlock.Type)

	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	require.NoError(t, err)
	privKey, ok := tlsCert.PrivateKey.(*ecdsa.PrivateKey)
	require.True(t, ok)
	assert.True(t, key.Equal(privKey))

	// Verify file permissions
	keyInfo, err := os.Stat(privateKeyPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), keyInfo.Mode().Perm())

	certInfo, err := os.Stat(sharedCertPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), certInfo.Mode().Perm())
}

func TestGenerateAndStore_ReusesExistingKeypair(t *testing.T) {
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "ca.crt")
	keyPath := filepath.Join(tmpDir, "ca.key")

	// First call generates
	first, err := GenerateAndStore(certPath, keyPath)
	require.NoError(t, err)

	// Second call reuses
	second, err := GenerateAndStore(certPath, keyPath)
	require.NoError(t, err)

	// Same cert bytes
	assert.Equal(t, first.Certificate[0], second.Certificate[0])

	// Same private key
	firstKey, ok := first.PrivateKey.(*ecdsa.PrivateKey)
	require.True(t, ok)
	secondKey, ok := second.PrivateKey.(*ecdsa.PrivateKey)
	require.True(t, ok)
	assert.True(t, firstKey.Equal(secondKey))
}

func TestGenerateAndStore_RegeneratesExpiredCert(t *testing.T) {
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "ca.crt")
	keyPath := filepath.Join(tmpDir, "ca.key")

	// Write an expired CA cert + key
	writeExpiredCA(t, certPath, keyPath)

	// Should regenerate
	cert, err := GenerateAndStore(certPath, keyPath)
	require.NoError(t, err)

	// Verify it's a fresh valid cert
	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err)
	assert.True(t, time.Now().Before(x509Cert.NotAfter))
}

func writeExpiredCA(t *testing.T, certPath, keyPath string) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "expired CA"},
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     time.Now().Add(-1 * time.Hour), // expired
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	require.NoError(t, os.WriteFile(certPath, certPEM, 0644))

	keyDER, err := x509.MarshalECPrivateKey(caKey)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0600))
}
