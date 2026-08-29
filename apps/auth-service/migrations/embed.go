// Package migrations exposes the Auth Service SQL migrations as an embedded
// filesystem. Defined inside the migrations/ directory itself so the
// //go:embed directive can reach the .sql files (Go's embed only sees files
// in the package's own directory).
//
// Consumers (infra/migrate, tests) import this package and pass migrations.FS
// to golang-migrate's iofs source. This pattern is reproduced by every
// service that owns a schema.
package migrations

import "embed"

// FS is the embedded Auth Service migrations. Each file is a golang-migrate
// versioned pair (NNNNNN_name.up.sql / .down.sql). The path passed to iofs.New
// is "." (the embed root is the migrations/ directory itself).
//
//go:embed *.sql
var FS embed.FS
