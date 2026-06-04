package ca

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

func TestGenerateAndStore(t *testing.T) {
	tmpDir := t.TempDir()
	sharedCertPath := filepath.Join(tmpDir, "shared", "ca.crt")
	privateKeyPath := filepath.Join(tmpDir, "private", "ca.key")

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

	// Verify private key dir permissions
	privDirInfo, err := os.Stat(filepath.Dir(privateKeyPath))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), privDirInfo.Mode().Perm())
}
