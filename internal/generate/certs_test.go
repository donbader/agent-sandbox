package generate

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateCA(t *testing.T) {
	outDir := t.TempDir()

	certPath, keyPath, err := GenerateCA(outDir)
	require.NoError(t, err)

	// Verify cert file exists and is valid PEM
	certPEM, err := os.ReadFile(certPath)
	require.NoError(t, err)
	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	assert.Equal(t, "CERTIFICATE", block.Type)

	// Parse the certificate
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	assert.True(t, cert.IsCA)
	assert.Equal(t, "agent-sandbox CA", cert.Subject.CommonName)
	assert.Equal(t, []string{"agent-sandbox"}, cert.Subject.Organization)

	// Verify key file exists and is valid PEM
	keyPEM, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	keyBlock, _ := pem.Decode(keyPEM)
	require.NotNil(t, keyBlock)
	assert.Equal(t, "EC PRIVATE KEY", keyBlock.Type)

	// Parse the key
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	require.NoError(t, err)
	assert.IsType(t, &ecdsa.PrivateKey{}, key)

	// Verify paths are in certs/ subdirectory
	assert.Equal(t, filepath.Join(outDir, "certs", "ca.crt"), certPath)
	assert.Equal(t, filepath.Join(outDir, "certs", "ca.key"), keyPath)

	// Verify key file permissions (0600)
	info, err := os.Stat(keyPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
