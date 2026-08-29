// Package featureflag evaluates feature flags per the spec's requirements:
// global, per-user, per-room, percentage rollout, environment-specific.
//
// Evaluation precedence (highest wins):
//
//  1. Explicit per-user override.
//  2. Explicit per-room override.
//  3. Percentage rollout (hash of (flag, user) → bucket).
//  4. Global default for the current environment.
//
// Persistence (Postgres-backed flag store with Redis cache) ships in the
// phase that first needs dynamic flags. Until then, the in-memory Store here
// covers tests and bootstrap.
package featureflag

import (
	"context"
	"hash/fnv"
)

// FlagKey is the stable identifier of a flag, e.g. "sync.p_controller".
type FlagKey string

// Variant is the value a flag resolves to. Most flags are boolean on/off;
// Variant is kept as a string so we can express multivariate tests later.
type Variant string

const (
	// VariantOff disables the flag for the evaluating context.
	VariantOff Variant = "off"
	// VariantOn enables the flag for the evaluating context.
	VariantOn Variant = "on"
	// VariantControl is the control arm of an A/B test.
	VariantControl Variant = "control"
	// VariantExperiment is the experimental arm of an A/B test.
	VariantExperiment Variant = "experiment"
)

// Flag is the configured state of a flag.
type Flag struct {
	Key FlagKey
	// Global default per environment. Empty environment key "default" applies
	// when no environment-specific entry exists.
	Defaults map[string]Variant
	// Percentage of users (0..100) routed to VariantOn. 0 disables rollout.
	RolloutPercent uint8
	// RolloutVariant is the variant the rollout serves. Defaults to "on".
	RolloutVariant Variant
	// UserOverrides pin a specific variant per user ID.
	UserOverrides map[string]Variant
	// RoomOverrides pin a specific variant per room ID.
	RoomOverrides map[string]Variant
}

// Context carries the inputs needed to evaluate a flag.
type Context struct {
	UserID      string
	RoomID      string
	Environment string // "local"|"dev"|"prod"
}

// Store is the source of flag definitions. The evaluator consults it per
// check; a cached Postgres-backed implementation ships later.
type Store interface {
	Get(ctx context.Context, key FlagKey) (Flag, bool, error)
}

// Evaluator resolves flags against a Store.
type Evaluator struct{ store Store }

// New returns an Evaluator backed by store.
func New(store Store) *Evaluator { return &Evaluator{store: store} }

// Evaluate resolves a flag for the given context. A missing flag returns
// VariantOff without error so callers can guard new code paths safely.
func (e *Evaluator) Evaluate(ctx context.Context, key FlagKey, ec Context) (Variant, error) {
	flag, ok, err := e.store.Get(ctx, key)
	if err != nil {
		return VariantOff, err
	}
	if !ok {
		return VariantOff, nil
	}

	// 1. Per-user override.
	if v, ok := flag.UserOverrides[ec.UserID]; ok {
		return v, nil
	}
	// 2. Per-room override.
	if v, ok := flag.RoomOverrides[ec.RoomID]; ok {
		return v, nil
	}
	// 3. Percentage rollout.
	if flag.RolloutPercent > 0 && ec.UserID != "" {
		bucket := bucket(key, ec.UserID)
		if bucket < flag.RolloutPercent {
			v := flag.RolloutVariant
			if v == "" {
				v = VariantOn
			}
			return v, nil
		}
	}
	// 4. Environment-specific default, falling back to "default".
	if v, ok := flag.Defaults[ec.Environment]; ok {
		return v, nil
	}
	if v, ok := flag.Defaults["default"]; ok {
		return v, nil
	}
	return VariantOff, nil
}

// bucket returns a stable 0..99 bucket for a (flag, user) pair. FNV is fast
// and well-distributed enough for rollouts; cryptographic strength is not
// required and would only cost CPU at scale.
func bucket(key FlagKey, userID string) uint8 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte(userID))
	return uint8(h.Sum32() % 100)
}

// IsOn is a convenience for boolean flags.
func (e *Evaluator) IsOn(ctx context.Context, key FlagKey, ec Context) (bool, error) {
	v, err := e.Evaluate(ctx, key, ec)
	return v == VariantOn, err
}

// MemoryStore is a simple in-memory Store for tests and bootstrap.
type MemoryStore struct {
	flags map[FlagKey]Flag
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore(flags ...Flag) *MemoryStore {
	m := &MemoryStore{flags: make(map[FlagKey]Flag, len(flags))}
	for _, f := range flags {
		m.flags[f.Key] = f
	}
	return m
}

// Get implements Store.
func (m *MemoryStore) Get(_ context.Context, key FlagKey) (Flag, bool, error) {
	f, ok := m.flags[key]
	return f, ok, nil
}
