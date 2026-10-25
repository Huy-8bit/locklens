// Package migrations holds the versioned SQL schema of the LockLens
// monitoring store. Files are named NNNN_description.sql and applied in order
// by internal/repository; each file runs in its own transaction.
package migrations

import "embed"

// FS contains every migration file.
//
//go:embed *.sql
var FS embed.FS
