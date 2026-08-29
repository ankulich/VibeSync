// Package migrations exposes the Sync Service SQL migrations.
package migrations

import "embed"

// FS is the embedded Sync Service migrations.
//
//go:embed *.sql
var FS embed.FS
