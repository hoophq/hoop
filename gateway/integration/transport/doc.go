//go:build integration

// Package transport holds the end-to-end integration test harness for the
// agent↔gateway transport. It boots a real gateway in-process (PostgreSQL
// container, migrations, plugin chain, and the production gRPC server on an
// ephemeral port via testutil.StartGateway) and drives it with a real agent
// controller and raw client streams over the wire.
//
// The scenarios are written against the transport-agnostic Connector seam
// (see connector_test.go), not against gRPC types directly, so the same
// scenario bodies can validate the upcoming WebSocket transport for
// behavioral parity: a WebSocket Connector is added to transports() and every
// test runs against both wires without being rewritten.
//
// Run with: make test-transport (or `go test -tags integration ./...`).
package transport
