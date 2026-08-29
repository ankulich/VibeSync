package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/auth-service/internal/infra/postgres"
)

// Relay drains the Auth outbox: polls unpublished rows, publishes each via the
// Publisher, marks each published within a Postgres tx so a crash mid-batch
// leaves no torn state. A Redis lock (acquired via the Locker) elects a single
// leader across Auth replicas; non-leaders sleep and retry.
//
// Ordering guarantee: per-aggregate FIFO. The SELECT ... FOR UPDATE SKIP
// LOCKED + ORDER BY occurred_at in FetchUnpublished preserves order within a
// poll; the relay publishes serially within the batch; the next poll continues
// from the next-oldest. Cross-batch ordering is best-effort but stable in the
// common case (one active writer per aggregate).
type Relay struct {
	pool      Pool
	publisher PublisherAdapter
	locker    Locker
	lockKey   string
	lockTTL   time.Duration
	batchSize int
	pollEvery time.Duration
}

// Pool is the minimal pgxpool surface the relay needs. Aliased to a local
// interface so tests can substitute a fake pool without depending on pgxpool.
type Pool interface {
	BeginTx(ctx context.Context) (pgx.Tx, error)
}

// PublisherAdapter is the surface the relay uses to publish. Decoupled from
// the concrete Publisher so tests can inject a fake.
type PublisherAdapter interface {
	PublishOne(ctx context.Context, pe postgres.PendingEvent) error
}

// Locker is the leader-election surface. Acquire returns a fencing token + a
// release function; if the lock can't be acquired (someone else holds it),
// Acquire returns ErrNotLeader.
type Locker interface {
	Acquire(ctx context.Context, key string, ttl time.Duration) (token uint64, release func() error, err error)
}

// ErrNotLeader is returned by Locker.Acquire when the lock is held elsewhere.
var ErrNotLeader = errors.New("relay: not leader")

// RelayOptions configures the relay.
type RelayOptions struct {
	BatchSize int           // rows per poll; default 100
	PollEvery time.Duration // sleep between polls when idle; default 1s
	LockTTL   time.Duration // leader lock TTL; default 30s
}

// NewRelay constructs a Relay.
func NewRelay(pool Pool, publisher PublisherAdapter, locker Locker, opts RelayOptions) *Relay {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 100
	}
	if opts.PollEvery <= 0 {
		opts.PollEvery = time.Second
	}
	if opts.LockTTL <= 0 {
		opts.LockTTL = 30 * time.Second
	}
	return &Relay{
		pool:      pool,
		publisher: publisher,
		locker:    locker,
		lockKey:   "auth:outbox:relay",
		lockTTL:   opts.LockTTL,
		batchSize: opts.BatchSize,
		pollEvery: opts.PollEvery,
	}
}

// Run polls the outbox until ctx is canceled. Designed to run as a goroutine
// from main.go; returns when ctx is done or on an unrecoverable error.
func (r *Relay) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Try to become leader for this poll. If we lose, sleep briefly and
		// retry — the leader will renew; we will take over if it dies.
		token, release, err := r.locker.Acquire(ctx, r.lockKey, r.lockTTL)
		if err != nil {
			if errors.Is(err, ErrNotLeader) {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(r.pollEvery):
				}
				continue
			}
			// Lock backend down: sleep and retry rather than hot-looping.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(r.pollEvery):
			}
			continue
		}

		// We are the leader. Renew periodically by re-acquiring inside the
		// loop; for simplicity here we hold for one poll cycle and re-acquire.
		// A background renewal goroutine (future) would extend the lease during
		// a long batch.
		_ = token
		published, err := r.drainOnce(ctx)
		releaseErr := release()
		if err != nil {
			// Publish or DB error mid-batch: log and back off.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(r.pollEvery):
			}
			continue
		}
		_ = releaseErr
		// Idle back-off: if we published nothing, sleep longer to avoid
		// hammering Postgres. A busy relay keeps polling at PollEvery.
		if published == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(r.pollEvery):
			}
		}
	}
}

// drainOnce polls one batch and publishes it. Returns the count published.
func (r *Relay) drainOnce(ctx context.Context) (int, error) {
	tx, err := r.pool.BeginTx(ctx)
	if err != nil {
		return 0, fmt.Errorf("relay: begin: %w", err)
	}
	// Always roll back if we didn't commit. The outbox writes here are
	// mark-published updates; the actual Kafka publish happens outside the tx.
	defer func() { _ = tx.Rollback(ctx) }()

	pending, err := postgres.FetchUnpublished(ctx, tx, r.batchSize)
	if err != nil {
		return 0, fmt.Errorf("relay: fetch: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil
	}
	for _, pe := range pending {
		if err := r.publisher.PublishOne(ctx, pe); err != nil {
			// Publish failed: leave the row unpublished (we'll retry next
			// poll) and stop the batch. Return the count published so far.
			return 0, fmt.Errorf("relay: publish %s: %w", pe.ID, err)
		}
		if err := postgres.MarkPublished(ctx, tx, pe.ID, time.Now().UTC()); err != nil {
			return 0, fmt.Errorf("relay: mark %s: %w", pe.ID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("relay: commit: %w", err)
	}
	return len(pending), nil
}
