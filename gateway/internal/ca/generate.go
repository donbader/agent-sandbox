package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// GenerateAndStore creates a fresh ECDSA P-256 CA keypair, writes the public cert
// to sharedCertPath (for the agent container) and the private key to privateKeyPath
// (gateway-internal only). Returns the parsed tls.Certificate for MITM use.
func GenerateAndStore(sharedCertPath, privateKeyPath string) (tls.Certificate, error) {
	// Generate CA private key
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generating CA key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generating serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"agent-sandbox"},
			CommonName:   "agent-sandbox CA",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	// Self-sign the CA certificate
	caDER, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("creating CA cert: %w", err)
	}

	// Ensure parent directories exist
	if err := os.MkdirAll(filepath.Dir(sharedCertPath), 0755); err != nil {
		return tls.Certificate{}, fmt.Errorf("creating shared cert dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(privateKeyPath), 0700); err != nil {
		return tls.Certificate{}, fmt.Errorf("creating private key dir: %w", err)
	}

	// Write CA cert (public, readable by agent via shared volume)
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caDER,
	})
	if err := os.WriteFile(sharedCertPath, certPEM, 0644); err != nil {
		return tls.Certificate{}, fmt.Errorf("writing CA cert: %w", err)
	}

	// Write CA key (private, gateway-internal only)
	keyDER, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("encoding CA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyDER,
	})
	if err := os.WriteFile(privateKeyPath, keyPEM, 0600); err != nil {
		return tls.Certificate{}, fmt.Errorf("writing CA key: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{caDER},
		PrivateKey:  caKey,
	}, nil
}
