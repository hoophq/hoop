// Package e2e runs the shipped sidecar binary against a real database.
//
// # Why this module exists
//
// Every other sidecar suite tests a component in isolation: a codec decodes
// a byte slice a test wrote, a gate evaluates a statement a test built. That
// is most of the value and it is fast, but it shares one blind spot — the
// test supplies the bytes, so it can only find bugs in code that reads them,
// never in the assumptions about how they arrive.
//
// Three bugs from the MySQL work (DEP-170) landed squarely in that blind
// spot, and two of them were invisible to a green unit suite:
//
//   - Masking silently did nothing. The gate built one codec per direction,
//     which is correct for Postgres and MSSQL and wrong for MySQL, whose
//     server decoding depends on capabilities and commands latched from the
//     client side. Every unit test drove one codec instance directly, so
//     none of them could see it. The relay came up, logged "masking: true",
//     denied statements correctly, and returned unmasked email addresses.
//
//   - An intermittent client hang. Against a server using
//     caching_sha2_password, the fast-auth reply and the OK that follows
//     arrive in ONE read; the rewriter, reading a phase the decoder had
//     already advanced past the end of that read, took an auth byte for a
//     column count and buffered the rest of the connection forever. It
//     reproduced in roughly two runs out of three and depended entirely on
//     real TCP read boundaries.
//
// Both were found by pointing a real driver at a real server. Neither was
// findable by writing more table-driven cases. That is the whole argument
// for the cost of this module.
//
// # What it actually runs
//
// The REAL artifact, not a library composition: `hoop-inspect` built from
// sidecar/cmd and launched as a subprocess against a mysql:8 container. That
// matters because masking is only reachable through the binary — the
// detection plugin is a nested module the root cannot import, so a test that
// called daemon.Run in-process would have to pass a nil Plugin and would
// silently test the unmasked path.
//
// mysql:8 rather than the MariaDB image the agent integration suite uses,
// deliberately. MariaDB negotiates mysql_native_password; the hang above
// only appears under caching_sha2_password, which is MySQL 8's default. The
// cheaper image would not have caught the bug this suite exists to catch.
//
// # Its own module
//
// testcontainers pulls in Docker's client library and most of its transitive
// tree. The sidecar root module has exactly one dependency and that is a
// product property, so this lives in a nested module like store/sqlite and
// pii/alcatraz do. It is reached by `make test-sidecar-e2e` and by its own CI
// job, never by `make test-oss`: a container boot does not belong in the unit
// run.
package e2e
