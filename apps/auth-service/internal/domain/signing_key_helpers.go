package domain

import (
	"crypto/rsa"
	"crypto/x509"
)

// encodePrivateKeyPKCS8 marshals an RSA private key to PKCS#8 DER. PKCS#8 is
// preferred over PKCS#1 (the legacy RSA-specific format) because it is the
// modern, algorithm-agnostic standard and is what crypto/tls and most key
// tooling expect. The DER bytes are what the KeyCipher encrypts at rest.
func encodePrivateKeyPKCS8(k *rsa.PrivateKey) ([]byte, error) {
	return x509.MarshalPKCS8PrivateKey(k)
}
