package proxy

// extractSNI extracts the Server Name Indication from a TLS ClientHello message.
func extractSNI(data []byte) string {
	// TLS record: type(1) + version(2) + length(2) + handshake
	if len(data) < 5 || data[0] != 0x16 {
		return "" // not a TLS handshake
	}

	// Handshake header: type(1) + length(3) + version(2) + random(32) + session_id
	pos := 5
	if pos >= len(data) || data[pos] != 0x01 {
		return "" // not ClientHello
	}
	pos += 4 // skip type + length

	if pos+2 > len(data) {
		return ""
	}
	pos += 2 // skip version

	pos += 32 // skip random

	if pos >= len(data) {
		return ""
	}
	sessionIDLen := int(data[pos])
	pos += 1 + sessionIDLen

	// Cipher suites
	if pos+2 > len(data) {
		return ""
	}
	cipherSuitesLen := int(data[pos])<<8 | int(data[pos+1])
	pos += 2 + cipherSuitesLen

	// Compression methods
	if pos >= len(data) {
		return ""
	}
	compMethodsLen := int(data[pos])
	pos += 1 + compMethodsLen

	// Extensions
	if pos+2 > len(data) {
		return ""
	}
	extensionsLen := int(data[pos])<<8 | int(data[pos+1])
	pos += 2

	end := min(pos+extensionsLen, len(data))

	for pos+4 <= end {
		extType := int(data[pos])<<8 | int(data[pos+1])
		extLen := int(data[pos+2])<<8 | int(data[pos+3])
		pos += 4

		if extType == 0 { // SNI extension
			if pos+2 > end {
				return ""
			}
			// SNI list length
			pos += 2

			if pos+3 > end {
				return ""
			}
			// nameType := data[pos]
			nameLen := int(data[pos+1])<<8 | int(data[pos+2])
			pos += 3

			if pos+nameLen > end {
				return ""
			}
			return string(data[pos : pos+nameLen])
		}

		pos += extLen
	}

	return ""
}
