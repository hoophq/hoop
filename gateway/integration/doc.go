//go:build integration

// Package integration holds Level-1 gateway smoke tests: they boot a real
// PostgreSQL container, run the full migration set, bootstrap the default
// organization, and drive the gateway's complete gin route tree through an
// in-process httptest server.
//
// No agent and no gRPC server are involved — these tests exercise the HTTP
// API, auth (local JWT + hpk_ API keys), RBAC middleware, the GORM model
// layer, and the migration bootstrap. They are the API-level counterpart to
// the wire-protocol suites under agent/integration.
//
// Run with: make test-gateway
package integration
