package crypto

import (
	"crypto/rsa"
	"encoding/base64"
)

// EncodeRSAJWKFields returns the (n, e) pair for a public key in the base64url
// form the proto JsonWebKey message expects. n is the modulus, e the exponent.
//
// Replaces the per-field EncodeRSAModulusB64URL / EncodeRSAExponentB64URL
// helpers; this single function is what app/keys.go calls.
func EncodeRSAJWKFields(key any) (n, e string) {
	pub, ok := key.(*rsa.PublicKey)
	if !ok {
		return "", ""
	}
	return encodeRSAModulus(pub), encodeRSAExponent(pub)
}

func encodeRSAModulus(pub *rsa.PublicKey) string {
	return base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
}

func encodeRSAExponent(pub *rsa.PublicKey) string {
	v := pub.E
	var buf []byte
	switch {
	case v == 0:
		buf = []byte{0}
	case v < 0x100:
		buf = []byte{byte(v)}
	case v < 0x10000:
		buf = []byte{byte(v >> 8), byte(v)}
	default:
		buf = []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	}
	for len(buf) > 1 && buf[0] == 0 {
		buf = buf[1:]
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}
