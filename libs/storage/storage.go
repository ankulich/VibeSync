// Package storage defines VibeSync's object-storage port (MinIO / S3).
//
// Used for: avatars, user uploads, FFmpeg-transcoded media cache. The
// concrete minio-go-backed implementation ships with the Storage Service
// (later phase). The contract here pins the surface so callers stay
// backend-agnostic.
package storage

import (
	"context"
	"io"
	"time"
)

// Bucket enumerates the storage classes VibeSync uses. Mapped to MinIO
// buckets (or S3 prefixes) by configuration.
type Bucket string

const (
	// BucketAvatar holds user profile pictures.
	BucketAvatar Bucket = "avatars"
	// BucketUpload holds user-uploaded media files.
	BucketUpload Bucket = "uploads"
	// BucketMediaCache holds FFmpeg-transcoded media cache entries.
	BucketMediaCache Bucket = "media-cache"
)

// Object identifies a stored blob.
type Object struct {
	Bucket   Bucket
	Key      string
	Size     int64
	MimeType string
}

// PutOptions influence an upload.
type PutOptions struct {
	ContentType string
	// TTL is the object lifetime; zero means persistent.
	TTL time.Duration
}

// Client is the storage port. Implementations wrap minio-go or aws-sdk-go.
type Client interface {
	// Put uploads r to bucket/key. The caller closes r.
	Put(ctx context.Context, bucket Bucket, key string, r io.Reader, opts PutOptions) (Object, error)
	// Get returns a reader for the object. The caller closes it.
	Get(ctx context.Context, bucket Bucket, key string) (io.ReadCloser, Object, error)
	// Delete removes the object. Idempotent: missing objects are not an error.
	Delete(ctx context.Context, bucket Bucket, key string) error
	// PresignUpload returns a short-lived URL a client may PUT to directly.
	PresignUpload(ctx context.Context, bucket Bucket, key string, ttl time.Duration) (string, error)
	// PresignDownload returns a short-lived URL a client may GET from.
	PresignDownload(ctx context.Context, bucket Bucket, key string, ttl time.Duration) (string, error)
}
