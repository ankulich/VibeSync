// Package migrations exposes the Media Service SQL migrations.
package migrations

import "embed"

// FS is the embedded Media Service migrations.
//
//go:embed *.sql
var FS embed.FS
