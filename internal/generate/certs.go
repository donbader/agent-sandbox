package generate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// GenerateCA creates a self-signed CA certificate and key, writing them to outDir/certs/.
// Returns the paths to the generated cert and key files.
func GenerateCA(outDir string) (certPath, keyPath string, err error) {
	certsDir := filepath.Join(outDir, "certs")
	if err := os.MkdirAll(certsDir, 0755); err != nil {
		return "", "", fmt.Errorf("creating certs dir: %w", err)
	}

	// Generate CA private key
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generating CA key: %w", err)
	}

	// Create CA certificate template
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", fmt.Errorf("generating serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"agent-sandbox"},
			CommonName:   "agent-sandbox CA",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	// Self-sign the CA certificate
	caDER, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	if err != nil {
		return "", "", fmt.Errorf("creating CA cert: %w", err)
	}

	// Write CA cert as PEM
	certPath = filepath.Join(certsDir, "ca.crt")
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caDER,
	})
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return "", "", fmt.Errorf("writing CA cert: %w", err)
	}

	// Write CA key as PEM
	keyPath = filepath.Join(certsDir, "ca.key")
	keyDER, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		return "", "", fmt.Errorf("encoding CA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyDER,
	})
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return "", "", fmt.Errorf("writing CA key: %w", err)
	}

	return certPath, keyPath, nil
}
