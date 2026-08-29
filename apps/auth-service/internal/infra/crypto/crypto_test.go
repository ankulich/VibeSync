package crypto

import (
	"context"
	"testing"
	"time"

	"vibesync/apps/auth-service/internal/domain"
	"vibesync/apps/auth-service/internal/ports"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	t.Parallel()
	h := NewPasswordHasher()
	hash, err := h.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !h.Compare(hash, "correct horse battery staple") {
		t.Error("Compare must accept the original plaintext")
	}
	if h.Compare(hash, "wrong password") {
		t.Error("Compare must reject a different plaintext")
	}
	// Different hashes for the same plaintext (random salt).
	hash2, _ := h.Hash("correct horse battery staple")
	if hash == hash2 {
		t.Error("Hash must be salted (same input → different output)")
	}
}

func TestPasswordHashRejectsMalformed(t *testing.T) {
	t.Parallel()
	h := NewPasswordHasher()
	if h.Compare("not-a-hash", "anything") {
		t.Error("Compare must return false for a malformed stored hash")
	}
}

func TestKeyCipherRoundTrip(t *testing.T) {
	t.Parallel()
	mk, err := GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewKeyCipher(mk)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("the private key bytes go here")
	ct, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if string(ct) == string(plaintext) {
		t.Error("ciphertext must differ from plaintext")
	}
	pt, err := c.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(pt) != string(plaintext) {
		t.Errorf("round-trip mismatch: got %q want %q", pt, plaintext)
	}
}

func TestKeyCipherRejectsWrongMaster(t *testing.T) {
	t.Parallel()
	mk1, _ := GenerateMasterKey()
	mk2, _ := GenerateMasterKey()
	c1, _ := NewKeyCipher(mk1)
	c2, _ := NewKeyCipher(mk2)
	ct, _ := c1.Encrypt([]byte("secret"))
	if _, err := c2.Decrypt(ct); err == nil {
		t.Error("Decrypt with wrong master key must fail")
	}
}

func TestNewKeyCipherRejectsBadSize(t *testing.T) {
	t.Parallel()
	if _, err := NewKeyCipher("dG9vIHNob3J0"); err == nil {
		t.Error("must reject a non-32-byte key")
	}
}

func TestJWTSignVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	cipher := mustCipher(t)
	now := time.Now().UTC()
	active, priv, err := domain.GenerateSigningKey(now, "vk1_test", cipher.Encrypt, RSAPublicKeyToJWK)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewJWTSigner(active, priv, nil)
	if err != nil {
		t.Fatal(err)
	}

	claims := accessTokenClaimsInputForTest("user_1", 2, "https://auth.vibesync.local", time.Hour)
	tok, err := signer.Sign(context.Background(), claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if tok == "" {
		t.Fatal("token must be non-empty")
	}
	got, err := signer.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Subject != "user_1" {
		t.Errorf("Subject = %q, want user_1", got.Subject)
	}
	if got.SystemRole != 2 {
		t.Errorf("SystemRole = %d, want 2", got.SystemRole)
	}
}

func TestJWTVerifyRejectsTampered(t *testing.T) {
	t.Parallel()
	signer := mustSigner(t)
	claims := accessTokenClaimsInputForTest("user_1", 2, "iss", time.Hour)
	tok, _ := signer.Sign(context.Background(), claims)
	// Tamper the payload (middle segment), not the signature. Corrupting the
	// payload changes what was signed → signature verification must fail.
	// Find the second '.' (start of signature) and corrupt a char before it.
	dotCount := 0
	tamperAt := -1
	for i, c := range tok {
		if c == '.' {
			dotCount++
			if dotCount == 2 {
				tamperAt = i - 1
				break
			}
		}
	}
	if tamperAt < 0 {
		t.Fatal("could not find payload/signature boundary in token")
	}
	// Flip the char at tamperAt to a different valid base64url char.
	orig := tok[tamperAt]
	flipped := byte('A')
	if orig == 'A' {
		flipped = 'B'
	}
	tampered := tok[:tamperAt] + string(flipped) + tok[tamperAt+1:]
	if _, err := signer.Verify(context.Background(), tampered); err == nil {
		t.Error("Verify must reject a payload-tampered token")
	}
}

func TestJWTVerifyRejectsExpired(t *testing.T) {
	t.Parallel()
	signer := mustSigner(t)
	claims := accessTokenClaimsInputForTest("user_1", 2, "iss", -time.Hour) // expired 1h ago
	tok, _ := signer.Sign(context.Background(), claims)
	if _, err := signer.Verify(context.Background(), tok); err == nil {
		t.Error("Verify must reject an expired token")
	}
}

func TestJWTReloadKeepsOldTokensVerifying(t *testing.T) {
	t.Parallel()
	signer := mustSigner(t)
	claims := accessTokenClaimsInputForTest("user_1", 2, "iss", time.Hour)
	oldTok, _ := signer.Sign(context.Background(), claims)

	// Capture the OLD key's public JWK from the signer's current JWKS before
	// we rotate, so the retired list matches the key that signed oldTok.
	oldJWKS := signer.JWKS()
	var oldPublic []byte
	for _, k := range oldJWKS {
		if k.KeyID == "vk1_test" {
			oldPublic, _ = k.MarshalJSON()
		}
	}
	if oldPublic == nil {
		t.Fatal("could not find old key in signer JWKS")
	}

	// Rotate: a new key, with the previous one now retired.
	cipher := mustCipher(t)
	now := time.Now().UTC()
	newKey, newPriv, err := domain.GenerateSigningKey(now, "vk1_new", cipher.Encrypt, RSAPublicKeyToJWK)
	if err != nil {
		t.Fatal(err)
	}
	retired := []RetiredKeyInput{{KID: "vk1_test", PublicJWK: oldPublic}}
	if err := signer.Reload(newKey, newPriv, retired); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	// Old token still verifies (retired key still in the verifier set).
	if _, err := signer.Verify(context.Background(), oldTok); err != nil {
		t.Errorf("old token must still verify after rotation: %v", err)
	}
	// New Sign calls use the new KID.
	if signer.ActiveKID() != "vk1_new" {
		t.Errorf("ActiveKID = %q, want vk1_new", signer.ActiveKID())
	}
}

// --- helpers ---

func mustCipher(t *testing.T) *KeyCipher {
	t.Helper()
	mk, _ := GenerateMasterKey()
	c, err := NewKeyCipher(mk)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustSigner(t *testing.T) *JWTSigner {
	t.Helper()
	cipher := mustCipher(t)
	now := time.Now().UTC()
	active, priv, err := domain.GenerateSigningKey(now, "vk1_test", cipher.Encrypt, RSAPublicKeyToJWK)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewJWTSigner(active, priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func accessTokenClaimsInputForTest(sub string, role int32, iss string, ttl time.Duration) ports.AccessTokenClaims {
	now := time.Now().UTC()
	return ports.AccessTokenClaims{
		Subject:    sub,
		SystemRole: role,
		Issuer:     iss,
		Audience:   []string{"vibesync"},
		IssuedAt:   now,
		ExpiresAt:  now.Add(ttl),
	}
}
