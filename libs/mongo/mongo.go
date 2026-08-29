// Package mongo defines VibeSync's MongoDB port for event/log/analytics
// storage.
//
// Mongo is used where the access pattern is append-only and query is
// ad-hoc/time-bucketed: analytics events, audit logs, sync heartbeat history.
// Transactional business state lives in Postgres. See ADR Databases.
//
// The concrete mongo-go-driver-backed implementation ships with the
// Notification/Analytics phases. The contract here pins the surface.
package mongo

import (
	"context"
	"time"
)

// Collection is the minimal surface callers use. Real drivers expose far
// more; we keep this narrow so repository code stays driver-agnostic.
type Collection interface {
	InsertOne(ctx context.Context, doc any) (id any, err error)
	Find(ctx context.Context, filter any) (Cursor, error)
	CountDocuments(ctx context.Context, filter any) (int64, error)
}

// Cursor is a forward-only result iterator.
type Cursor interface {
	Next(ctx context.Context) bool
	Decode(val any) error
	Close(ctx context.Context) error
	Err() error
}

// IndexSpec describes a collection index. Services call EnsureIndexes at
// startup to declare their access patterns; the implementation is idempotent.
type IndexSpec struct {
	Keys       map[string]int // field name → 1 (asc) or -1 (desc)
	Unique     bool
	Background bool
	Name       string
}

// TimeBucket is a helper for analytics queries that bucket events by time.
// Most dashboards want per-minute or per-hour rollups; centralizing the
// truncation avoids per-call mistakes.
func TimeBucket(t time.Time, granularity time.Duration) time.Time {
	if granularity <= 0 {
		return t
	}
	nanos := t.UnixNano()
	bucket := nanos - (nanos % int64(granularity))
	return time.Unix(0, bucket).UTC()
}
