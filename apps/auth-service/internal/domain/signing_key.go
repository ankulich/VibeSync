package domain

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"time"
)

// SigningKey is a JWT signing key with its encrypted private side. See
// ADR-0012: private keys are AES-GCM-encrypted at rest with a master key from
// env; the public side is served via JWKS for cross-service verification.
type SigningKey struct {
	KID              string
	Status           SigningKeyStatus
	PrivateEncrypted []byte // AES-GCM ciphertext; decrypt with master key
	PublicJWK        []byte // canonical JWK JSON (public part only)
	CreatedAt        time.Time
	RetiredAt        time.Time
}

// RSAKeyBits is the signing-key size. 2048 is sufficient for RS256 within our
// token lifetimes and is the smallest size jwks consumers universally accept.
const RSAKeyBits = 2048

// ErrNoActiveKey is returned when no active signing key exists. The use case
// bootstraps one on first startup; this error means bootstrap was skipped or
// raced.
var ErrNoActiveKey = errors.New("signing key: no active key")

// GenerateSigningKey produces a fresh RSA keypair and a SigningKey ready for
// storage. The private key is returned separately so the caller can use it
// in-process without re-decrypting; storage receives only PrivateEncrypted.
//
// encryptFn is the KeyCipher.Encrypt port (AES-GCM with the master key).
// jwkFn serializes the RSA public key to canonical JWK JSON (the
// go-jose-backed adapter implements this).
func GenerateSigningKey(
	now time.Time,
	kid string,
	encryptFn func(plaintext []byte) ([]byte, error),
	jwkFn func(pub *rsa.PublicKey) ([]byte, error),
) (SigningKey, *rsa.PrivateKey, error) {
	priv, err := rsa.GenerateKey(rand.Reader, RSAKeyBits)
	if err != nil {
		return SigningKey{}, nil, err
	}
	privDER, err := encodePrivateKeyPKCS8(priv)
	if err != nil {
		return SigningKey{}, nil, err
	}
	enc, err := encryptFn(privDER)
	if err != nil {
		return SigningKey{}, nil, err
	}
	jwk, err := jwkFn(&priv.PublicKey)
	if err != nil {
		return SigningKey{}, nil, err
	}
	return SigningKey{
		KID:              kid,
		Status:           SigningKeyStatusActive,
		PrivateEncrypted: enc,
		PublicJWK:        jwk,
		CreatedAt:        now,
	}, priv, nil
}

// Retire transitions an active key to retired. Called by Rotate.
func (k *SigningKey) Retire(now time.Time) {
	k.Status = SigningKeyStatusRetired
	k.RetiredAt = now
}

// KIDPrefix is a short stable prefix for human-readable key IDs in logs.
// Actual KIDs are base64url random.
const KIDPrefix = "vk1_"

// NewKID returns a fresh key id. Random so guessing the next kid is not a
// side-channel for anything (the public JWK is served openly anyway), but
// uniqueness without DB coordination is convenient.
func NewKID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return KIDPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}
