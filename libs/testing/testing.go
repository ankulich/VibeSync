// Package testing provides test helpers for VibeSync: testcontainers-backed
// Postgres/Redis/Mongo/Kafka harnesses and fake implementations of the ports
// in libs/store, libs/redislib, etc.
//
// IMPORTANT: this package is named "testing" intentionally to mirror the Go
// standard library convention for test helpers. It MUST be imported with an
// alias (e.g. `vbtest "vibesync/libs/testing"`) to avoid clashing with the
// std "testing" package. The package-level doc comment above is the only
// place where the name is used unqualified.
//
// Concrete testcontainer wiring lands in Phase 4 with the first integration
// test. This file ships the small, dependency-free helpers that ship today.
package testing

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
	"time"
)

// SkipShort skips t when -test.short is set OR when the VIBE_TEST_INTEGRATION
// env var is not "1". Use at the top of any test that needs real containers.
//
// Integration tests are the source of truth for cross-service behavior, but
// they're slow and need Docker. Guarding them here keeps `go test ./...`
// fast in the inner loop while CI runs the full suite with
// VIBE_TEST_INTEGRATION=1.
func SkipShort(t *testing.T) {
	t.Helper()
	if testingShort() {
		t.Skip("skipping integration test in -short mode")
	}
	if os.Getenv("VIBE_TEST_INTEGRATION") != "1" {
		t.Skip("skipping integration test; set VIBE_TEST_INTEGRATION=1 to run")
	}
}

// testingShort is split out so we can stub it (it references the std testing
// package's Short(), which we don't import here to keep the cycle clean).
// Defined in testing_short.go.
var testingShort = func() bool { return false }

// UniqueName returns a stable-ish unique name for a container or database,
// scoped to the test so parallel tests don't collide. Format:
// "vibesync_<test>_<rand>".
func UniqueName(t *testing.T) string {
	t.Helper()
	name := strings.ToLower(t.Name())
	name = strings.NewReplacer(
		"/", "_", " ", "_", ":", "_", ".", "_",
	).Replace(name)
	if len(name) > 40 {
		name = name[len(name)-40:]
	}
	return fmt.Sprintf("vibesync_%s_%s", name, randString(6))
}

// randString returns a lowercase alphanumeric string of length n. Using
// math/rand/v2's auto-seeded source avoids the global-state pitfalls of the
// older rand package.
func randString(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rand.IntN(len(alphabet))]
	}
	return string(b)
}

// WaitFor polls fn until it returns nil or timeout elapses. Used by
// testcontainer setup to wait for readiness. The interval is fixed at 250ms
// which balances startup speed against host load under parallel tests.
func WaitFor(ctx context.Context, timeout time.Duration, fn func(ctx context.Context) error) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := fn(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr == nil {
		return fmt.Errorf("wait: timed out after %s", timeout)
	}
	return fmt.Errorf("wait: timed out after %s: %w", timeout, lastErr)
}
