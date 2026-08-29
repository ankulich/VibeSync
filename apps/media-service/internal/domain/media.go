// Package domain contains the Media Service's domain entities.
package domain

import (
	"errors"
	"strings"
	"time"
)

// MediaKind mirrors the proto MediaKind enum (audio/video).
type MediaKind int8

// MediaSource mirrors the proto MediaSource enum (provider/upload/cache).
type MediaSource int8

const (
	// KindAudio is an audio-only media item.
	KindAudio MediaKind = 1
	// KindVideo is a video media item.
	KindVideo MediaKind = 2

	// SourceProvider means the media comes from Spotify/YouTube (Phase 10).
	SourceProvider MediaSource = 1
	// SourceUpload means user-uploaded media via the Storage Service.
	SourceUpload MediaSource = 2
	// SourceCache means FFmpeg-transcoded local cache.
	SourceCache MediaSource = 3
)

// Media is a catalog item.
type Media struct {
	ID          string
	Kind        MediaKind
	Source      MediaSource
	ExternalRef string
	Title       string
	Artist      string
	DurationMs  int64
	CoverURL    string
	CreatedAt   time.Time
}

// QueueItem is one entry in a room's media queue.
type QueueItem struct {
	RoomID   string
	Position int
	MediaID  string
	AddedAt  time.Time
}

// Errors.
var (
	ErrMediaTitleEmpty = errors.New("media: title must not be empty")
)

// NewMediaParams holds user-supplied fields for creating a media item.
type NewMediaParams struct {
	Kind        MediaKind
	Source      MediaSource
	ExternalRef string
	Title       string
	Artist      string
	DurationMs  int64
	CoverURL    string
}

// NewMedia constructs a Media in a valid state.
func NewMedia(now time.Time, id string, p NewMediaParams) (Media, error) {
	title := strings.TrimSpace(p.Title)
	if title == "" {
		return Media{}, ErrMediaTitleEmpty
	}
	kind := p.Kind
	if kind == 0 {
		kind = KindAudio
	}
	source := p.Source
	if source == 0 {
		source = SourceProvider
	}
	return Media{
		ID: id, Kind: kind, Source: source,
		ExternalRef: strings.TrimSpace(p.ExternalRef),
		Title:       title, Artist: strings.TrimSpace(p.Artist),
		DurationMs: p.DurationMs, CoverURL: strings.TrimSpace(p.CoverURL),
		CreatedAt: now,
	}, nil
}
