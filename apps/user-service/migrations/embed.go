// Package migrations exposes the User Service SQL migrations as an embedded
// filesystem. Defined inside the migrations/ directory so the //go:embed
// directive can reach the .sql files. Reproduces the auth-service pattern.
package migrations

import "embed"

// FS is the embedded User Service migrations.
//
//go:embed *.sql
var FS embed.FS
