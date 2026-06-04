package mitm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/http"
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

func TestHandler_Matches(t *testing.T) {
	ca := testCA(t)
	h := NewHandler([]string{"api.telegram.org", "example.com"}, ca, nil)

	if !h.Matches("api.telegram.org") {
		t.Error("expected match for api.telegram.org")
	}
	if !h.Matches("example.com") {
		t.Error("expected match for example.com")
	}
	if h.Matches("other.com") {
		t.Error("expected no match for other.com")
	}
}

func TestTelegramRewriter_RewriteRequest(t *testing.T) {
	// Set env for test
	t.Setenv("TELEGRAM_BOT_TOKEN", "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11")

	rw, err := NewTelegramRewriter()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("rewrites bot token in path", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "https://api.telegram.org/botREDACTED_TELEGRAM_TOKEN/getUpdates", nil)
		ok := rw.RewriteRequest(req)
		if !ok {
			t.Error("expected rewrite to succeed")
		}
		expected := "/bot123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11/getUpdates"
		if req.URL.Path != expected {
			t.Errorf("expected path %q, got %q", expected, req.URL.Path)
		}
	})

	t.Run("ignores non-bot paths", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "https://api.telegram.org/other/path", nil)
		ok := rw.RewriteRequest(req)
		if ok {
			t.Error("expected rewrite to not match")
		}
	})

	t.Run("ignores paths without method", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "https://api.telegram.org/botTOKEN", nil)
		ok := rw.RewriteRequest(req)
		if ok {
			t.Error("expected rewrite to not match path without method slash")
		}
	})
}
