package app

import (
	"context"
	"crypto/rsa"

	"connectrpc.com/connect"
	"github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5"

	"vibesync/apps/auth-service/internal/domain"
	"vibesync/apps/auth-service/internal/infra/crypto"
	authv1 "vibesync/gen/go/vibesync/auth/v1"
	vberr "vibesync/libs/errors"
)

// GetJwks returns the active + retired public signing keys in JWKS form. This
// endpoint is unauthenticated (other services fetch it to verify tokens);
// rate-limited by the web middleware.
func (s *Service) GetJwks(ctx context.Context, _ *connect.Request[authv1.GetJwksRequest]) (*connect.Response[authv1.GetJwksResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	keys, err := s.listSigningKeyJWK(ctx)
	if err != nil {
		return nil, err
	}
	resp := &authv1.GetJwksResponse{Keys: keys}
	return connect.NewResponse(resp), nil
}

// listSigningKeyJWK loads verifiable keys from the repo and projects them to
// the proto JsonWebKey shape. Reads happen in a short read-tx.
func (s *Service) listSigningKeyJWK(ctx context.Context) ([]*authv1.JsonWebKey, error) {
	var keys []*authv1.JsonWebKey
	err := s.readTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		stored, err := s.keys.ListVerifiable(ctx, tx)
		if err != nil {
			return err
		}
		for _, k := range stored {
			jwk, err := jwkFromStored(k)
			if err != nil {
				return err
			}
			keys = append(keys, jwk)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// jwkFromStored projects a stored SigningKey's public JWK JSON into the proto
// JsonWebKey message. The stored form is canonical JWK JSON; we parse out the
// n + e fields the proto expects.
func jwkFromStored(k domain.SigningKey) (*authv1.JsonWebKey, error) {
	var parsed jose.JSONWebKey
	if err := parsed.UnmarshalJSON(k.PublicJWK); err != nil {
		return nil, vberr.Internal("JWK_PARSE_FAILED", err.Error()).WithCause(err)
	}
	n, e := crypto.EncodeRSAJWKFields(parsed.Key)
	return &authv1.JsonWebKey{
		Kid: k.KID,
		Kty: "RSA",
		Alg: "RS256",
		Use: "sig",
		N:   n,
		E:   e,
	}, nil
}

// RotateKeys generates a new signing key, marks the previous active key
// retired, and loads it into the in-memory signer. Operator-only (gated by
// the API Gateway via ActionRotateKeys; this method assumes the caller is
// authorized).
//
// force=true rotates even if the active key is younger than the rotation
// interval (useful for incident response).
func (s *Service) RotateKeys(ctx context.Context, req *connect.Request[authv1.RotateKeysRequest]) (*connect.Response[authv1.RotateKeysResponse], error) {
	if err := ctxDone(ctx); err != nil {
		return nil, err
	}
	newKID, err := s.rotateSigningKey(ctx, req.Msg.GetForce())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv1.RotateKeysResponse{NewKid: newKID}), nil
}

// rotateSigningKey performs the rotation: generate new keypair → encrypt
// private side → insert as active → mark previous active retired → reload the
// signer. All DB writes in one tx so a mid-rotation crash cannot leave two
// active keys (the partial unique index would also reject that).
func (s *Service) rotateSigningKey(ctx context.Context, force bool) (string, error) {
	now := s.now()
	newKID, err := domain.NewKID()
	if err != nil {
		return "", vberr.Internal("KID_GEN_FAILED", err.Error()).WithCause(err)
	}
	newKey, newPriv, err := domain.GenerateSigningKey(now, newKID, s.cipher.Encrypt, crypto.RSAPublicKeyToJWK)
	if err != nil {
		return "", vberr.Internal("KEY_GEN_FAILED", err.Error()).WithCause(err)
	}

	var previousKID string
	if err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Mark the previous active key retired (if any).
		prev, err := s.keys.GetActive(ctx, tx)
		if err == nil {
			previousKID = prev.KID
			if !force && now.Sub(prev.CreatedAt) < s.cfg.Auth.KeyRotationInterval {
				// Too soon and not forced: refuse, leave the existing key.
				return vberr.FailedPrecondition("vibesync.auth", "ROTATION_TOO_SOON", "active key is younger than the rotation interval")
			}
			if err := s.keys.MarkRetired(ctx, tx, prev.KID, now); err != nil {
				return err
			}
		} else if !isNotFound(err) {
			return err
		}
		// Insert the new active key.
		return s.keys.Upsert(ctx, tx, newKey)
	}); err != nil {
		return "", err
	}

	// Reload the signer's key set in-process so new Sign calls use newKID and
	// Verify continues to accept both. The signer is the in-memory
	// *crypto.JWTSigner; main.go wired it as ports.TokenSigner. Reload via the
	// optional Reloader interface if the signer supports it.
	if reloader, ok := s.signer.(signerReloader); ok {
		var verifiable []crypto.RetiredKeyInput
		// Fetch the full verifiable set (active + retired) and decrypt the
		// active key's private side for the signer.
		if err := s.readTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			stored, err := s.keys.ListVerifiable(ctx, tx)
			if err != nil {
				return err
			}
			for _, k := range stored {
				if k.KID == newKey.KID {
					continue // active: passed separately with the decrypted priv key
				}
				verifiable = append(verifiable, crypto.RetiredKeyInput{KID: k.KID, PublicJWK: k.PublicJWK})
			}
			return nil
		}); err != nil {
			return "", err
		}
		if err := reloader.Reload(newKey, newPriv, verifiable); err != nil {
			return "", vberr.Internal("SIGNER_RELOAD_FAILED", err.Error()).WithCause(err)
		}
	}
	_ = previousKID
	return newKID, nil
}

// signerReloader is the optional interface a TokenSigner implements if it can
// hot-swap its active key without a process restart. *crypto.JWTSigner
// implements it; tests can substitute a signer that does not.
type signerReloader interface {
	Reload(active domain.SigningKey, activePriv *rsa.PrivateKey, retired []crypto.RetiredKeyInput) error
}
