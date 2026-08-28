// Package migrations owns the control plane schema: the numbered SQL, the
// runner that applies it, and the check that the binary matches it.
//
// Create a new migration with:
//
//	migrate create -ext sql -dir controlplane/backend/internal/migrations -seq <description>
//
// Both .up.sql and .down.sql are required; test the down path with
// `controlplane migrate down` before the PR. Read the next number from the
// directory listing, not from a count — this sequence must stay gapless.
// The numbering is independent of gateway/migrations: separate schemas,
// separate version tables.
package migrations

import "embed"

// FS holds the migrations compiled into the binary, so deployments need no
// migration files on disk.
//
//go:embed *.sql
var FS embed.FS
