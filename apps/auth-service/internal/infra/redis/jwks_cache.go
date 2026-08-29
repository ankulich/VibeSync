package redis

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// SourcedJWKS is the contract the cache consults to fetch a fresh JWKS from
// the JWT signer when the cache is empty or stale. Decoupling the source from
// the cache lets the cache be unit-tested without a real signer.
type SourcedJWKS interface {
	JWKSJSON() ([]byte, error)
}

// JWKSCache memoizes the JWKS served by GetJwks for a short TTL. The JWT
// signer regenerates the JWKS per call (cheap but non-zero), and the public
// API may receive many requests/second; caching keeps that cost flat.
//
// The cache is single-flight: a stampede on the TTL boundary still results in
// exactly one source fetch. Stale-while-revalidate is intentional: a fetch
// failure serves the previous value until a hard deadline.
type JWKSCache struct {
	source SourcedJWKS
	ttl    time.Duration

	mu        sync.Mutex
	cached    atomic.Pointer[cachedJWKS]
	refreshIn atomic.Bool
}

type cachedJWKS struct {
	json      []byte
	fetchedAt time.Time
}

// NewJWKSCache constructs a cache backed by source with the given TTL.
func NewJWKSCache(source SourcedJWKS, ttl time.Duration) *JWKSCache {
	return &JWKSCache{source: source, ttl: ttl}
}

// Get returns the cached JWKS JSON, refreshing if stale. The first call after
// startup pays the fetch; subsequent calls within TTL hit the cache.
func (c *JWKSCache) Get(ctx context.Context) ([]byte, error) {
	cur := c.cached.Load()
	if cur != nil && time.Since(cur.fetchedAt) < c.ttl {
		return cur.json, nil
	}
	// Stale or empty: refresh under single-flight.
	if !c.refreshIn.CompareAndSwap(false, true) {
		// Another goroutine is refreshing; serve stale if available, else wait.
		if cur != nil {
			return cur.json, nil
		}
		// No stale value: wait for the in-flight refresh via mutex.
		c.mu.Lock()
		defer c.mu.Unlock()
		if cur := c.cached.Load(); cur != nil {
			return cur.json, nil
		}
		return c.fetchLocked(), nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	defer c.refreshIn.Store(false)
	return c.fetchLocked(), nil
}

func (c *JWKSCache) fetchLocked() []byte {
	raw, err := c.source.JWKSJSON()
	if err != nil {
		// Stale-while-error: serve the previous value if it exists, so a
		// transient source failure does not blank out JWKS for clients.
		if cur := c.cached.Load(); cur != nil {
			return cur.json
		}
		return nil
	}
	// Pretty-print once so cache payloads are byte-identical across refreshes
	// (lets a CDN treat them as stable).
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err == nil {
		if b, err := json.Marshal(normalized); err == nil {
			raw = b
		}
	}
	c.cached.Store(&cachedJWKS{json: raw, fetchedAt: time.Now()})
	return raw
}
