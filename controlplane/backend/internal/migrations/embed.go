// Package migrations owns the control plane schema: the numbered SQL, the
// runner that applies it, and the check that the running binary matches it.
//
// The SQL lives next to the code that runs it rather than in a sibling
// package, so "how do I add a migration" and "what happens when it runs" are
// the same file listing.
//
// Create a new migration with:
//
//	migrate create -ext sql -dir controlplane/backend/internal/migrations -seq <description>
//
// Both .up.sql and .down.sql are required, and the down path must be tested
// with `controlplane migrate down` before the PR. A migration whose down was
// never run is a migration that cannot be rolled back during an incident.
//
// Read the next number from the directory listing, not from a count. This
// sequence has no gaps today and should not acquire any, but the gateway's
// does, and the habit of counting is what put them there.
//
// These numbers are independent of gateway/migrations. The two products own
// separate Postgres schemas and separate golang-migrate version tables, so
// the sequences cannot collide.
package migrations

import "embed"

// FS holds the migrations compiled into the binary, so a deployment never
// needs migration files on disk and the schema always matches the binary.
//
//go:embed *.sql
var FS embed.FS
