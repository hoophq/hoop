package models

import (
	"context"
	"database/sql/driver"
	"fmt"

	"github.com/hoophq/hoop/common/log"
	"gorm.io/gorm"
)

// RunOnOneReplica runs fn unless another replica is running the job named
// name, and reports whether it ran. Fleet-wide jobs (the startup mirror
// reconcile, the sidecar session reaper) do the same work on every replica;
// with one replica doing it the database does it once.
//
// The lock is a session advisory lock held on a connection taken from the
// pool for fn's duration, so a replica that dies releases it with its
// connection. fn reads and writes through its own connections, not this one.
//
// A pool of one connection (the embedded PGlite backend) runs fn directly:
// holding the lock would take the only connection fn needs, and there is
// only one replica.
func RunOnOneReplica(ctx context.Context, db *gorm.DB, name string, fn func(ctx context.Context) error) (bool, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return false, err
	}
	if sqlDB.Stats().MaxOpenConnections == 1 {
		return true, fn(ctx)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("failed taking a connection for the %s lock: %w", name, err)
	}
	defer conn.Close()
	var got bool
	err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext('hoop.replica'), hashtext($1))`, name).
		Scan(&got)
	if err != nil {
		return false, fmt.Errorf("failed taking the %s lock: %w", name, err)
	}
	if !got {
		return false, nil
	}
	defer func() {
		_, err := conn.ExecContext(context.WithoutCancel(ctx),
			`SELECT pg_advisory_unlock(hashtext('hoop.replica'), hashtext($1))`, name)
		if err != nil {
			log.Warnf("failed releasing the %s lock, closing its connection, reason=%v", name, err)
			// ErrBadConn makes database/sql close the connection rather than
			// pool it, and Postgres drops the lock with the session.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	return true, fn(ctx)
}
