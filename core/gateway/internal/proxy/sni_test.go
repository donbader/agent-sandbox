package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractSNI(t *testing.T) {
	t.Run("valid ClientHello with SNI", func(t *testing.T) {
		// Minimal TLS 1.2 ClientHello with SNI "example.com"
		hello := buildClientHello("example.com")
		sni := extractSNI(hello)
		assert.Equal(t, "example.com", sni)
	})

	t.Run("no SNI extension", func(t *testing.T) {
		// Just a TLS record header with handshake type but no extensions
		data := []byte{0x16, 0x03, 0x01, 0x00, 0x05, 0x01, 0x00, 0x00, 0x01, 0x00}
		sni := extractSNI(data)
		assert.Equal(t, "", sni)
	})

	t.Run("not TLS", func(t *testing.T) {
		data := []byte("GET / HTTP/1.1\r\n")
		sni := extractSNI(data)
		assert.Equal(t, "", sni)
	})

	t.Run("empty data", func(t *testing.T) {
		sni := extractSNI([]byte{})
		assert.Equal(t, "", sni)
	})
}

// buildClientHello constructs a minimal TLS ClientHello with the given SNI.
func buildClientHello(serverName string) []byte {
	sniBytes := []byte(serverName)
	sniLen := len(sniBytes)

	// SNI extension: type(0x00,0x00) + length + SNI list length + entry(type + length + name)
	sniExt := []byte{
		0x00, 0x00, // extension type: server_name
		byte((sniLen + 5) >> 8), byte((sniLen + 5) & 0xff), // extension length
		byte((sniLen + 3) >> 8), byte((sniLen + 3) & 0xff), // SNI list length
		0x00,                                // name type: host_name
		byte(sniLen >> 8), byte(sniLen & 0xff), // name length
	}
	sniExt = append(sniExt, sniBytes...)

	// Extensions total
	extLen := len(sniExt)
	extensions := []byte{byte(extLen >> 8), byte(extLen & 0xff)}
	extensions = append(extensions, sniExt...)

	// ClientHello body: version(2) + random(32) + session_id_len(1) + cipher_suites_len(2) + cipher(2) + comp_len(1) + comp(1) + extensions
	body := []byte{
		0x03, 0x03, // version: TLS 1.2
	}
	body = append(body, make([]byte, 32)...) // random
	body = append(body, 0x00)                // session ID length: 0
	body = append(body, 0x00, 0x02)          // cipher suites length: 2
	body = append(body, 0x00, 0x2f)          // TLS_RSA_WITH_AES_128_CBC_SHA
	body = append(body, 0x01)                // compression methods length: 1
	body = append(body, 0x00)                // null compression
	body = append(body, extensions...)

	// Handshake header: type(1) + length(3)
	handshakeLen := len(body)
	handshake := []byte{
		0x01, // ClientHello
		byte(handshakeLen >> 16), byte(handshakeLen >> 8), byte(handshakeLen & 0xff),
	}
	handshake = append(handshake, body...)

	// TLS record: type(1) + version(2) + length(2)
	recordLen := len(handshake)
	record := []byte{
		0x16,       // handshake
		0x03, 0x01, // TLS 1.0 (record layer version)
		byte(recordLen >> 8), byte(recordLen & 0xff),
	}
	record = append(record, handshake...)

	return record
}
