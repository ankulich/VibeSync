// Package crypto contains the cryptographic adapter implementations: argon2id
// password hashing, AES-GCM at-rest encryption for signing keys, and the JWT
// signer/verifier.
//
// All three are ports.PasswordHasher / ports.KeyCipher / ports.TokenSigner
// implementations. Centralizing them in one package keeps the crypto policy
// in one place — parameter changes happen here, not scattered across the
// codebase.
package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2 parameters. Chosen per the OWASP 2024 recommendation for interactive
// logins: 19 MiB memory, 2 iterations, 1 lane, 32-byte salt + 32-byte tag.
// Tunable via config in a later phase if threat model or hardware changes.
const (
	argon2Memory     = 19 * 1024 // KiB → 19 MiB
	argon2Iterations = 2
	argon2Lanes      = 1
	argon2SaltBytes  = 16
	argon2KeyBytes   = 32
)

// PHCVersion tags every hash we emit, so Compare can route by algorithm. The
// format is PHC-string-compatible: $argon2id$v=19$m=...,t=...,p=...$<salt>$<hash>.
const phcVersion = "argon2id"

// PasswordHasher implements ports.PasswordHasher using argon2id.
type PasswordHasher struct{}

// NewPasswordHasher returns a PasswordHasher.
func NewPasswordHasher() *PasswordHasher { return &PasswordHasher{} }

// Hash derives an argon2id hash of plaintext and returns it PHC-encoded.
// The salt is random per call, so the same plaintext hashes differently
// each time — important for both security and tests that compare hashes.
func (PasswordHasher) Hash(plaintext string) (string, error) {
	salt := make([]byte, argon2SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: salt: %w", err)
	}
	key := argon2.IDKey([]byte(plaintext), salt,
		argon2Iterations, argon2Memory, argon2Lanes, argon2KeyBytes)
	return phcEncode(salt, key), nil
}

// Compare reports whether plaintext matches a previously-hashed value, in
// constant time. Returns false (not an error) on any decode failure or
// mismatch — callers branch on the bool, not on error type.
func (PasswordHasher) Compare(hash, plaintext string) bool {
	salt, want, err := phcDecode(hash)
	if err != nil {
		// Malformed stored hash: treat as no-match. Operations should monitor
		// this branch; it indicates corruption or a downgrade attack.
		return false
	}
	got := argon2.IDKey([]byte(plaintext), salt,
		argon2Iterations, argon2Memory, argon2Lanes, argon2KeyBytes)
	// subtle.ConstantTimeCompare returns 1 iff equal AND same length.
	return subtle.ConstantTimeCompare(want, got) == 1
}

// phcEncode produces the PHC-string form we store.
func phcEncode(salt, key []byte) string {
	return fmt.Sprintf("$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		phcVersion, argon2.Version, argon2Memory, argon2Iterations, argon2Lanes,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

// phcDecode parses a PHC string back into (salt, key). Returns an error if
// the algorithm or version is not ours — refusing unknown formats is a
// security property, not a bug.
func phcDecode(s string) (salt, key []byte, err error) {
	parts := strings.Split(s, "$")
	// "$argon2id$v=...$m=...$salt$hash" splits to ["", "argon2id", "v=...", "m=...", "salt", "hash"].
	if len(parts) != 6 || parts[1] != phcVersion {
		return nil, nil, errors.New("password: unrecognized hash format")
	}
	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, fmt.Errorf("password: salt decode: %w", err)
	}
	key, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, fmt.Errorf("password: key decode: %w", err)
	}
	return salt, key, nil
}
