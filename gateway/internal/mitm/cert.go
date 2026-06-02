package mitm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"sync"
	"time"
)

// CertCache caches generated TLS certificates for MITM domains.
type CertCache struct {
	mu    sync.RWMutex
	certs map[string]tls.Certificate
}

// NewCertCache creates a new certificate cache.
func NewCertCache() *CertCache {
	return &CertCache{
		certs: make(map[string]tls.Certificate),
	}
}

// GetOrCreate returns a cached cert or generates a new one signed by the CA.
func (c *CertCache) GetOrCreate(domain string, caCert tls.Certificate) (tls.Certificate, error) {
	c.mu.RLock()
	if cert, ok := c.certs[domain]; ok {
		c.mu.RUnlock()
		return cert, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if cert, ok := c.certs[domain]; ok {
		return cert, nil
	}

	cert, err := generateCert(domain, caCert)
	if err != nil {
		return tls.Certificate{}, err
	}

	c.certs[domain] = cert
	return cert, nil
}

// generateCert creates a TLS certificate for the given domain, signed by the CA.
func generateCert(domain string, caCert tls.Certificate) (tls.Certificate, error) {
	// Parse CA certificate
	ca, err := x509.ParseCertificate(caCert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse CA cert: %w", err)
	}

	caKey, ok := caCert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return tls.Certificate{}, fmt.Errorf("CA private key is not ECDSA")
	}

	// Generate key for the new cert
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate key: %w", err)
	}

	// Create certificate template
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: domain,
		},
		DNSNames:  []string{domain},
		NotBefore: time.Now().Add(-1 * time.Hour),
		NotAfter:  time.Now().Add(24 * time.Hour),
		KeyUsage:  x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
	}

	// Sign with CA
	certDER, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("sign cert: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}, nil
}
