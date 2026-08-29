package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// KeyCipher implements ports.KeyCipher using AES-256-GCM. The master key is
// supplied as base64-encoded 32 bytes (typically via the VB_AUTH_KEY_MASTER
// env var). Per-message nonces are random and prepended to the ciphertext.
//
// The cipher is constructed once at startup and held for the process
// lifetime; rotating the master key requires re-encrypting every signing key
// row (a future operational task, not a hot path).
type KeyCipher struct {
	aead cipher.AEAD
}

// NewKeyCipher constructs a KeyCipher from a base64-encoded 32-byte master
// key. Returns an error if the key is the wrong size or malformed.
func NewKeyCipher(masterKeyB64 string) (*KeyCipher, error) {
	masterKey, err := base64.StdEncoding.DecodeString(masterKeyB64)
	if err != nil {
		return nil, fmt.Errorf("keycipher: master key not valid base64: %w", err)
	}
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("keycipher: master key must be 32 bytes (got %d)", len(masterKey))
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("keycipher: aes new: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("keycipher: gcm new: %w", err)
	}
	return &KeyCipher{aead: aead}, nil
}

// Encrypt produces nonce||ciphertext for plaintext. The nonce is 12 bytes
// (GCM standard) and random per call; the same plaintext encrypts to a
// different ciphertext each time.
func (c *KeyCipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("keycipher: nonce: %w", err)
	}
	// Seal appends the ciphertext+tag to dst (the nonce), so the result is
	// nonce||ciphertext||tag — a self-contained blob ready for storage.
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt. Returns an error if the ciphertext is malformed
// or fails authentication — the latter indicates tampering or a wrong master
// key, both of which must fail loud.
func (c *KeyCipher) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < c.aead.NonceSize() {
		return nil, errors.New("keycipher: ciphertext too short")
	}
	nonce, ct := ciphertext[:c.aead.NonceSize()], ciphertext[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("keycipher: decrypt: %w", err)
	}
	return plaintext, nil
}

// GenerateMasterKey produces a fresh random 32-byte master key, base64-encoded.
// Used by the bootstrap script (scripts/gen-master-key.sh, future) so operators
// can mint a master key for VB_AUTH_KEY_MASTER without writing their own tool.
func GenerateMasterKey() (string, error) {
	k := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, k); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k), nil
}
