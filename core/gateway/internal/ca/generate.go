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
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// GenerateAndStore loads an existing CA keypair if both files exist, or creates
// a fresh ECDSA P-256 CA keypair. The public cert is written to sharedCertPath
// (for the agent container) and the private key to privateKeyPath (gateway-internal).
// Returns the parsed tls.Certificate for MITM use.
func GenerateAndStore(sharedCertPath, privateKeyPath string) (tls.Certificate, error) {
	// Try loading existing keypair first
	if cert, err := loadExisting(sharedCertPath, privateKeyPath); err == nil {
		slog.Info("reusing existing CA keypair", "cert", sharedCertPath)
		return cert, nil
	}

	slog.Info("generating new CA keypair")

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
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
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

// loadExisting attempts to load a CA cert+key from disk.
// Returns an error if either file is missing or unparseable.
func loadExisting(certPath, keyPath string) (tls.Certificate, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return tls.Certificate{}, err
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parsing existing CA keypair: %w", err)
	}

	// Verify it hasn't expired
	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parsing CA certificate: %w", err)
	}
	if time.Now().After(x509Cert.NotAfter) {
		return tls.Certificate{}, fmt.Errorf("CA certificate expired at %s", x509Cert.NotAfter)
	}

	return cert, nil
}
