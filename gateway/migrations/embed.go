// Package migrations embeds the gateway SQL migration files into the
// binary. They are applied at startup through golang-migrate's iofs source
// (see gateway/models/bootstrap), so a deployment never needs migration
// files on disk and the schema always matches the binary that runs it.
//
// Create a new migration with:
//
//	migrate create -ext sql -dir gateway/migrations -seq <description>
//
// Numbering must stay sequential; both .up.sql and .down.sql are required.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
