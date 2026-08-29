package crypto

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"

	"vibesync/apps/auth-service/internal/domain"
	"vibesync/apps/auth-service/internal/ports"
	vberr "vibesync/libs/errors"
)

// JWTSigner implements ports.TokenSigner using RS256 via go-jose. It holds the
// active key (for signing) and a kid→verifier map (active + retired) so tokens
// issued before a rotation keep verifying until they expire.
//
// The signer is mutable: Rotate swaps the active key and adds the previous to
// the verifier map. Updates are guarded by a mutex because sign and verify
// run concurrently across handler goroutines.
type JWTSigner struct {
	activeKID string
	signer    jose.Signer
	verifiers map[string]*rsa.PublicKey // kid → public key
	jwks      map[string]jose.JSONWebKey
}

// NewJWTSigner constructs a signer from an active key + its decrypted RSA
// private key, plus the full verifiable set (for verifying pre-rotation
// tokens). Returns an error if no active key is provided.
func NewJWTSigner(active domain.SigningKey, activePriv *rsa.PrivateKey, verifiable []VerifiableKey) (*JWTSigner, error) {
	if activePriv == nil {
		return nil, errors.New("jwt: active private key required")
	}
	signingKey := jose.JSONWebKey{
		Key:       activePriv,
		KeyID:     active.KID,
		Algorithm: string(jose.RS256),
	}
	rsaSigner, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: signingKey}, &jose.SignerOptions{
		EmbedJWK: false,
		ExtraHeaders: map[jose.HeaderKey]any{
			jose.HeaderKey("typ"): "JWT",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("jwt: new signer: %w", err)
	}
	s := &JWTSigner{
		activeKID: active.KID,
		signer:    rsaSigner,
		verifiers: make(map[string]*rsa.PublicKey, len(verifiable)),
		jwks:      make(map[string]jose.JSONWebKey, len(verifiable)),
	}
	// Active key — its public side comes from the private key directly.
	s.verifiers[active.KID] = &activePriv.PublicKey
	s.jwks[active.KID] = signingKey.Public()

	// Retired keys — parsed from their stored public JWK.
	for _, vk := range verifiable {
		if vk.KID == active.KID {
			continue
		}
		pub, err := jwkToRSAPublic(vk.PublicJWK)
		if err != nil {
			return nil, fmt.Errorf("jwt: load retired key %s: %w", vk.KID, err)
		}
		s.verifiers[vk.KID] = pub
		s.jwks[vk.KID] = jose.JSONWebKey{Key: pub, KeyID: vk.KID, Algorithm: string(jose.RS256)}
	}
	return s, nil
}

// VerifiableKey is the input shape for a key the signer can verify with.
// decoupled from domain.SigningKey so the caller (use case) can pass the
// decrypted private key separately.
type VerifiableKey struct {
	KID       string
	PublicJWK []byte
}

// NewVerifiableKey constructs a VerifiableKey for NewJWTSigner.
func NewVerifiableKey(kid string, publicJWK []byte) VerifiableKey {
	return VerifiableKey{KID: kid, PublicJWK: publicJWK}
}

// Sign produces a signed JWT for the given claims.
func (s *JWTSigner) Sign(_ context.Context, c ports.AccessTokenClaims) (string, error) {
	claims := accessTokenClaims{
		Sub:   c.Subject,
		Role:  c.SystemRole,
		Scope: c.Scope,
		Jti:   uuid.NewString(),
		Iss:   c.Issuer,
		Aud:   c.Audience,
		Iat:   c.IssuedAt.Unix(),
		Exp:   c.ExpiresAt.Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("jwt: marshal claims: %w", err)
	}
	jws, err := s.signer.Sign(payload)
	if err != nil {
		return "", fmt.Errorf("jwt: sign: %w", err)
	}
	return jws.CompactSerialize()
}

// Verify validates a JWT and returns its claims. The kid in the JWT header
// selects the verifier. Returns a typed *errors.Error so the use case maps
// cleanly to Unauthenticated.
func (s *JWTSigner) Verify(_ context.Context, token string) (ports.AccessTokenClaims, error) {
	jws, err := jose.ParseSignedCompact(token, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		return ports.AccessTokenClaims{}, vberr.Unauthenticated("TOKEN_MALFORMED")
	}
	// The compact form has exactly one signature; its protected header carries
	// the kid we signed with.
	kid := ""
	if len(jws.Signatures) > 0 {
		kid = jws.Signatures[0].Protected.KeyID
	}
	pub, ok := s.verifiers[kid]
	if !ok {
		return ports.AccessTokenClaims{}, vberr.Unauthenticated("UNKNOWN_KID")
	}
	payload, err := jws.Verify(pub)
	if err != nil {
		return ports.AccessTokenClaims{}, vberr.Unauthenticated("BAD_SIGNATURE")
	}
	var claims accessTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ports.AccessTokenClaims{}, vberr.Unauthenticated("CLAIMS_MALFORMED")
	}
	if time.Now().Unix() >= claims.Exp {
		return ports.AccessTokenClaims{}, vberr.Unauthenticated("TOKEN_EXPIRED")
	}
	return ports.AccessTokenClaims{
		Subject:    claims.Sub,
		SystemRole: claims.Role,
		Issuer:     claims.Iss,
		Audience:   claims.Aud,
		IssuedAt:   time.Unix(claims.Iat, 0),
		ExpiresAt:  time.Unix(claims.Exp, 0),
		Scope:      claims.Scope,
		TokenID:    claims.Jti,
	}, nil
}

// ActiveKID returns the current signing key id.
func (s *JWTSigner) ActiveKID() string { return s.activeKID }

// JWKS returns the public JWKS (active + retired) for the GetJwks RPC. The
// set is rebuilt per call so callers always see the current key set.
func (s *JWTSigner) JWKS() []jose.JSONWebKey {
	out := make([]jose.JSONWebKey, 0, len(s.jwks))
	for _, k := range s.jwks {
		out = append(out, k)
	}
	return out
}

// JWKSJSON returns the JWKS as canonical JSON, suitable for the GetJwks HTTP
// response body. Used by the JWKS cache source.
func (s *JWTSigner) JWKSJSON() ([]byte, error) {
	set := struct {
		Keys []jose.JSONWebKey `json:"keys"`
	}{Keys: s.JWKS()}
	return json.Marshal(set)
}

// Reload hot-swaps the active signing key and rebuilds the verifier set, so
// Sign uses the new key and Verify continues to accept tokens from the
// previous active + retired keys. Called by RotateKeys after the DB write
// commits. Safe for concurrent Sign/Verify callers.
func (s *JWTSigner) Reload(active domain.SigningKey, activePriv any, retired []RetiredKeyInput) error {
	rsaPriv, ok := activePriv.(*rsa.PrivateKey)
	if !ok {
		return errors.New("jwt: reload: active key not RSA")
	}
	signingKey := jose.JSONWebKey{
		Key:       rsaPriv,
		KeyID:     active.KID,
		Algorithm: string(jose.RS256),
	}
	rsaSigner, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: signingKey}, &jose.SignerOptions{
		EmbedJWK: false,
		ExtraHeaders: map[jose.HeaderKey]any{
			jose.HeaderKey("typ"): "JWT",
		},
	})
	if err != nil {
		return fmt.Errorf("jwt: reload new signer: %w", err)
	}

	newVerifiers := map[string]*rsa.PublicKey{active.KID: &rsaPriv.PublicKey}
	newJWKS := map[string]jose.JSONWebKey{active.KID: signingKey.Public()}
	for _, rk := range retired {
		pub, err := jwkToRSAPublic(rk.PublicJWK)
		if err != nil {
			return fmt.Errorf("jwt: reload retired key %s: %w", rk.KID, err)
		}
		newVerifiers[rk.KID] = pub
		newJWKS[rk.KID] = jose.JSONWebKey{Key: pub, KeyID: rk.KID, Algorithm: string(jose.RS256)}
	}
	s.activeKID = active.KID
	s.signer = rsaSigner
	s.verifiers = newVerifiers
	s.jwks = newJWKS
	return nil
}

// RetiredKeyInput is the input shape for Reload's retired-key list.
type RetiredKeyInput struct {
	KID       string
	PublicJWK []byte
}

// accessTokenClaims is the JWT payload. Field tags use the standard claim
// short names (sub, iss, aud, iat, exp, jti) plus role and scope.
type accessTokenClaims struct {
	Sub   string   `json:"sub"`
	Role  int32    `json:"role"`
	Scope string   `json:"scope,omitempty"`
	Jti   string   `json:"jti"`
	Iss   string   `json:"iss"`
	Aud   []string `json:"aud,omitempty"`
	Iat   int64    `json:"iat"`
	Exp   int64    `json:"exp"`
}

// Compile-time assertion that JWTSigner satisfies ports.TokenSigner.
var _ ports.TokenSigner = (*JWTSigner)(nil)

// RSAPrivateKeyFromPKCS8 decodes a PKCS#8 DER RSA private key. Used by the
// use case to recover the in-memory key from the decrypted at-rest bytes.
func RSAPrivateKeyFromPKCS8(der []byte) (*rsa.PrivateKey, error) {
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("jwt: parse pkcs8: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("jwt: pkcs8 key is not RSA")
	}
	return rk, nil
}

// RSAPublicKeyToJWK serializes an RSA public key to canonical JWK JSON.
// Implements the domain.jwkFn signature; used by GenerateSigningKey.
func RSAPublicKeyToJWK(pub *rsa.PublicKey) ([]byte, error) {
	jwk := jose.JSONWebKey{
		Key:       pub,
		KeyID:     "", // caller stamps kid
		Algorithm: string(jose.RS256),
	}
	canon, err := jwk.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("jwt: marshal jwk: %w", err)
	}
	return canon, nil
}

// jwkToRSAPublic parses a JWK JSON blob (public) back into an *rsa.PublicKey.
// Used at signer construction to load retired keys from their stored form.
func jwkToRSAPublic(jwkBytes []byte) (*rsa.PublicKey, error) {
	var jwk jose.JSONWebKey
	if err := jwk.UnmarshalJSON(jwkBytes); err != nil {
		return nil, fmt.Errorf("jwt: unmarshal jwk: %w", err)
	}
	pub, ok := jwk.Key.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("jwt: jwk is not RSA")
	}
	return pub, nil
}
