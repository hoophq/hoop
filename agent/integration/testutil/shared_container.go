//go:build integration

package testutil

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/testcontainers/testcontainers-go"
)

// Package-scoped upstream containers, booted once and shared by every test.
//
// # Why
//
// The suite used to boot one container per test. Measured on CI, that was
// roughly 70% of its 571s runtime and is what pushed it past the 10 minute
// `go test -timeout` budget (ENG-511). SQL Server dominated: 16 tests at
// ~14s of boot each, so TestMSSQL_Ping spent 14.18s to send one ping.
//
// # How isolation is kept
//
// Each test gets a private database on the shared server instead of a
// private server. That is the same boundary the connection counters
// already use — MSSQLContainer.countSessionsOn, MySQLContainer.ProcessCount
// and PGBackendCount all filter on the database name — so assertions like
// "exactly 0 upstream sessions remain after SessionClose" stay exact
// rather than counting a neighbouring test's traffic. Every test also
// generates unique table and collection names already, so nothing depends
// on starting from an empty schema.
//
// Forked databases are not dropped. The container is terminated when the
// package finishes, and dropping a SQL Server database with live sessions
// needs an ALTER ... SET SINGLE_USER WITH ROLLBACK IMMEDIATE dance that
// buys nothing here.
//
// # Lifecycle
//
// ShutdownSharedContainers must run after the last test; TestMain in
// package integration calls it. Every boot registers its terminator
// through registerSharedShutdown, so a container that is never booted
// (running `go test -run TestSSH_...`, say) costs nothing.

// databaseSeq names forked databases. A counter rather than a timestamp so
// a failure message points at a database you can locate by test order in
// the -v log.
var databaseSeq atomic.Uint64

// nextDatabaseName returns a fresh database identifier. It is the only
// producer of forked database names, which is what makes it safe to
// interpolate the result straight into a CREATE DATABASE statement.
func nextDatabaseName() string {
	return fmt.Sprintf("testdb_%d", databaseSeq.Add(1))
}

var (
	sharedShutdownMu    sync.Mutex
	sharedShutdownFuncs []func()
)

// registerSharedShutdown records a terminator to run at package teardown.
func registerSharedShutdown(fn func()) {
	sharedShutdownMu.Lock()
	defer sharedShutdownMu.Unlock()
	sharedShutdownFuncs = append(sharedShutdownFuncs, fn)
}

// terminateAtPackageEnd registers c for termination once the package's
// tests are done. Boot helpers call this instead of t.Cleanup, which would
// tear the container down after the first test that used it.
func terminateAtPackageEnd(c testcontainers.Container) {
	registerSharedShutdown(func() {
		_ = c.Terminate(context.Background())
	})
}

// ShutdownSharedContainers terminates every shared upstream container the
// suite booted. Call it from TestMain after m.Run. It is a no-op when no
// test needed a container, and safe to call more than once.
func ShutdownSharedContainers() {
	sharedShutdownMu.Lock()
	fns := sharedShutdownFuncs
	sharedShutdownFuncs = nil
	sharedShutdownMu.Unlock()

	for _, fn := range fns {
		fn()
	}
}
