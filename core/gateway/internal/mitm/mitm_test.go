package mitm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

func testCA(t *testing.T) tls.Certificate {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	return tls.Certificate{
		Certificate: [][]byte{caDER},
		PrivateKey:  caKey,
	}
}

func TestCertCache_GetOrCreate(t *testing.T) {
	ca := testCA(t)
	cache := NewCertCache()

	// First call should generate a cert
	cert1, err := cache.GetOrCreate("example.com", ca)
	if err != nil {
		t.Fatal(err)
	}
	if len(cert1.Certificate) == 0 {
		t.Fatal("expected certificate to be generated")
	}

	// Parse and verify the generated cert
	parsed, err := x509.ParseCertificate(cert1.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Subject.CommonName != "example.com" {
		t.Errorf("expected CN=example.com, got %s", parsed.Subject.CommonName)
	}
	if len(parsed.DNSNames) != 1 || parsed.DNSNames[0] != "example.com" {
		t.Errorf("expected DNSNames=[example.com], got %v", parsed.DNSNames)
	}

	// Second call should return cached cert
	cert2, err := cache.GetOrCreate("example.com", ca)
	if err != nil {
		t.Fatal(err)
	}
	if &cert1.Certificate[0][0] != &cert2.Certificate[0][0] {
		t.Error("expected cached certificate to be returned")
	}
}

func TestCertCache_RegeneratesExpiredCert(t *testing.T) {
	ca := testCA(t)
	cache := NewCertCache()

	// Generate a valid cert and manually expire it in the cache
	cert, err := cache.GetOrCreate("example.com", ca)
	if err != nil {
		t.Fatal(err)
	}

	// Replace with an already-expired cert
	expiredCert := makeExpiredCert(t, ca)
	cache.mu.Lock()
	cache.certs["example.com"] = expiredCert
	cache.mu.Unlock()

	// GetOrCreate should detect the expired cert and regenerate
	renewed, err := cache.GetOrCreate("example.com", ca)
	if err != nil {
		t.Fatal(err)
	}

	// Verify we got a new cert, not the expired one
	parsedRenewed, err := x509.ParseCertificate(renewed.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}

	if time.Now().After(parsedRenewed.NotAfter) {
		t.Error("expected renewed cert to be valid, but it is expired")
	}

	// Verify it's different from the expired cert (different serial number)
	parsedExpired, _ := x509.ParseCertificate(expiredCert.Certificate[0])
	if parsedRenewed.SerialNumber.Cmp(parsedExpired.SerialNumber) == 0 {
		t.Error("expected a new cert to be generated, got the same expired cert back")
	}

	// Verify it's also different from the original (proves cache was updated)
	parsedOriginal, _ := x509.ParseCertificate(cert.Certificate[0])
	if parsedRenewed.SerialNumber.Cmp(parsedOriginal.SerialNumber) == 0 {
		t.Error("expected a freshly generated cert, got the original")
	}
}

// makeExpiredCert generates a cert that expired in the past.
func makeExpiredCert(t *testing.T, caCert tls.Certificate) tls.Certificate {
	t.Helper()

	ca, err := x509.ParseCertificate(caCert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	caKey, ok := caCert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatal("CA private key is not ECDSA")
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	serialNumber, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: "example.com"},
		DNSNames:     []string{"example.com"},
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     time.Now().Add(-1 * time.Hour), // expired 1 hour ago
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}
}

func TestHandler_Matches(t *testing.T) {
	ca := testCA(t)
	h := NewHandler([]string{"api.example.com", "other.example.com"}, ca)

	if !h.Matches("api.example.com") {
		t.Error("expected match for api.example.com")
	}
	if !h.Matches("other.example.com") {
		t.Error("expected match for other.example.com")
	}
	if h.Matches("unknown.com") {
		t.Error("expected no match for unknown.com")
	}
}

func TestHandler_TransportReuse(t *testing.T) {
	ca := testCA(t)
	h := NewHandler([]string{"example.com"}, ca)

	t1 := h.getTransport("example.com")
	t2 := h.getTransport("example.com")
	if t1 != t2 {
		t.Error("expected same transport to be reused for same host")
	}

	t3 := h.getTransport("other.com")
	if t1 == t3 {
		t.Error("expected different transport for different hosts")
	}
}
