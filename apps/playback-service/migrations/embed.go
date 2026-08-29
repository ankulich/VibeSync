// Package migrations exposes the Playback Service SQL migrations.
package migrations

import "embed"

// FS is the embedded Playback Service migrations.
//
//go:embed *.sql
var FS embed.FS
