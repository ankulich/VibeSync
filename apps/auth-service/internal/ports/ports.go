// Package ports defines the interfaces the Auth use cases depend on. Each
// port is implemented by an adapter in internal/infra. The split keeps use
// cases testable in isolation (swap any port for a fake) and lets the infra
// layer evolve without rippling into business logic.
//
// Conventions:
//   - Every method takes a context.Context as its first arg.
//   - Repository methods return (entity, error) or (slice, error). Not-found
//     is reported as a typed error (see ErrNotFound below), NOT a zero value
//   - nil error.
//   - Mutations within a transaction accept a Tx arg (from the TxRunner port)
//     so the use case controls atomicity.
package ports

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"vibesync/apps/auth-service/internal/domain"
	vboutbox "vibesync/libs/outbox"
)

// ErrNotFound is the canonical not-found error returned by repositories. Use
// errors.Is to detect; the use case maps it to *errors.NotFound for clients.
type errNotFound struct{ entity, id string }

// NotFound constructs a typed not-found error.
func NotFound(entity, id string) error { return errNotFound{entity, id} }

func (e errNotFound) Error() string { return e.entity + " not found: " + e.id }

// ErrNotFound is the sentinel compared via errors.Is for not-found errors
// returned by repositories.
var ErrNotFound = errNotFound{}

func (errNotFound) Is(target error) bool {
	_, ok := target.(errNotFound)
	return ok
}

// --- primitive ports ----------------------------------------------------

// Clock returns the current time. Injected so tests can pin time without
// touching the global clock.
type Clock interface {
	Now() time.Time
}

// IDGen generates canonical ULIDs. Wraps libs/id.
type IDGen interface {
	New() string
}

// PasswordHasher is the argon2id port. Hash is used at user creation and at
// refresh-token construction; Compare is the constant-time verifier.
type PasswordHasher interface {
	Hash(plaintext string) (string, error)
	Compare(hash, plaintext string) bool
}

// KeyCipher is the AES-GCM at-rest encryption port for signing keys.
type KeyCipher interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
}

// TokenSigner issues and verifies access tokens (JWTs, RS256). The signer
// holds the active key for signing and the active+retired set for verifying.
type TokenSigner interface {
	// Sign returns a signed JWT for the given claims.
	Sign(ctx context.Context, claims AccessTokenClaims) (string, error)
	// Verify validates a JWT and returns its claims, or a typed *errors.Error.
	Verify(ctx context.Context, token string) (AccessTokenClaims, error)
	// ActiveKID returns the KID of the current signing key.
	ActiveKID() string
}

// AccessTokenClaims is the canonical claim set embedded in every access token.
// Fields mirror IntrospectResponse (user_id, system_role, expires_at, scope).
type AccessTokenClaims struct {
	Subject    string   // user id (ULID)
	SystemRole int32    // vibesync.common.v1.SystemRole numeric value
	Issuer     string   // matches auth.issuer config
	Audience   []string // services allowed to consume this token
	IssuedAt   time.Time
	ExpiresAt  time.Time
	Scope      string // space-delimited per OAuth2
	TokenID    string // jti; unique per token
}

// OAuthProvider abstracts an external OAuth2 provider. Each provider (Spotify,
// Google) implements this; the registry resolves by name.
type OAuthProvider interface {
	// Name is the stable provider identifier ("spotify", "google").
	Name() string
	// AuthorizationURL builds the provider's auth URL with state + PKCE.
	AuthorizationURL(ctx context.Context, redirectURI, state, codeChallenge string) (string, error)
	// Exchange swaps an authorization code for provider tokens.
	Exchange(ctx context.Context, code, redirectURI, codeVerifier string) (ProviderTokens, error)
	// Profile fetches the user profile using the provider tokens.
	Profile(ctx context.Context, tokens ProviderTokens) (domain.ProviderProfile, error)
}

// ProviderTokens is the raw token response from a provider's token endpoint.
// Only the fields the Auth flow needs are normalized; the rest are discarded.
type ProviderTokens struct {
	AccessToken  string
	IDToken      string // present for OpenID Connect providers (Google)
	RefreshToken string // provider refresh, not our refresh; stored for provider API calls later
	ExpiresIn    time.Duration
}

// --- repositories -------------------------------------------------------

// UserRepo is the User aggregate persistence port.
type UserRepo interface {
	Create(ctx context.Context, tx pgx.Tx, u domain.User) error
	GetByID(ctx context.Context, tx pgx.Tx, id string) (domain.User, error)
	GetByEmail(ctx context.Context, tx pgx.Tx, email string) (domain.User, error)
	Update(ctx context.Context, tx pgx.Tx, u domain.User) error
}

// SessionRepo is the Session persistence port.
type SessionRepo interface {
	Create(ctx context.Context, tx pgx.Tx, s domain.Session) error
	GetByID(ctx context.Context, tx pgx.Tx, id string) (domain.Session, error)
	UpdateLastSeen(ctx context.Context, tx pgx.Tx, id string, at time.Time) error
	Revoke(ctx context.Context, tx pgx.Tx, id string, at time.Time) error // marks session ended; family kept
}

// RefreshTokenRepo is the refresh-token persistence port. The reuse-detection
// methods are first-class because they drive the security-critical family
// revocation.
type RefreshTokenRepo interface {
	Create(ctx context.Context, tx pgx.Tx, t domain.RefreshToken) error
	GetBySelector(ctx context.Context, tx pgx.Tx, selector string) (domain.RefreshToken, error)
	MarkUsed(ctx context.Context, tx pgx.Tx, id string, at time.Time) error
	MarkRevoked(ctx context.Context, tx pgx.Tx, id string, at time.Time) error
	// RevokeFamily marks every token in the family as compromised. Used when
	// reuse is detected. Atomic with the rest of the refresh transaction.
	RevokeFamily(ctx context.Context, tx pgx.Tx, familyID string, at time.Time) (int64, error)
}

// SigningKeyRepo is the JWT signing-key persistence port.
type SigningKeyRepo interface {
	Upsert(ctx context.Context, tx pgx.Tx, k domain.SigningKey) error
	GetActive(ctx context.Context, tx pgx.Tx) (domain.SigningKey, error)
	ListVerifiable(ctx context.Context, tx pgx.Tx) ([]domain.SigningKey, error) // active + retired
	MarkRetired(ctx context.Context, tx pgx.Tx, kid string, at time.Time) error
}

// OAuthFlowRepo persists transient OAuth flow state.
type OAuthFlowRepo interface {
	Create(ctx context.Context, tx pgx.Tx, f domain.OAuthFlow) error
	GetAndConsume(ctx context.Context, tx pgx.Tx, state string) (domain.OAuthFlow, error) // single-use
	Delete(ctx context.Context, tx pgx.Tx, state string) error
	DeleteExpired(ctx context.Context, tx pgx.Tx, before time.Time) (int64, error)
}

// OAuthAccountRepo persists links between users and provider identities.
type OAuthAccountRepo interface {
	Upsert(ctx context.Context, tx pgx.Tx, a domain.OAuthAccount) error
	GetByProvider(ctx context.Context, tx pgx.Tx, provider, providerUserID string) (domain.OAuthAccount, error)
	GetByUser(ctx context.Context, tx pgx.Tx, userID, provider string) (domain.OAuthAccount, error)
	ListByUser(ctx context.Context, tx pgx.Tx, userID string) ([]domain.OAuthAccount, error)
}

// OutboxWriter is the transactional-outbox port. Wraps libs/outbox.Writer but
// takes pgx.Tx so the use case can stage events in the same tx as the domain
// write. See ADR-0004.
type OutboxWriter interface {
	Append(ctx context.Context, tx pgx.Tx, events ...vboutbox.Event) error
}

// Pool is the connection-pool + tx-runner surface. The use case accepts this
// and runs every multi-statement mutation through InTx for atomicity.
type Pool interface {
	BeginTx(ctx context.Context) (pgx.Tx, error)
	Pool() *pgxpool.Pool
	Close()
}
