// Package migrations exposes the Provider Service SQL migrations.
package migrations

import "embed"

// FS is the embedded Provider Service migrations.
//
//go:embed *.sql
var FS embed.FS
