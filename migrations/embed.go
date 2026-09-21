// Package migrations contains the versioned PostgreSQL schema embedded in the migrator.
package migrations

import "embed"

// Files contains SQL migrations so the compiled migrator works without a source checkout.
//
//go:embed *.sql
var Files embed.FS
