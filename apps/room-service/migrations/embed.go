// Package migrations exposes the Room Service SQL migrations.
package migrations

import "embed"

// FS is the embedded Room Service migrations.
//
//go:embed *.sql
var FS embed.FS
