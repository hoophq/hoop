// Package database owns the connection pool and nothing else.
//
// It is deliberately not a models package. The gateway has one package
// holding the pool, the global handle, every entity and every query, and it
// grows without bound because there is no rule about what belongs in it.
// Here, each feature owns its own tables, its own types and its own queries,
// in its own package, next to the handler that serves them. This package
// hands them a *gorm.DB and gets out of the way.
//
// Two rules apply to every feature package that uses the handle:
//
//   - Take *gorm.DB as a parameter. There is no global here, so the mistake
//     is not available to make.
//   - Propagate gorm.ErrRecordNotFound. The caller decides whether a missing
//     row is a 404 or an empty list. Returning (nil, nil) for a missing row
//     is how the gateway ended up with callers that cannot tell "absent" from
//     "broken".
//
// Nothing here calls AutoMigrate. Schema lives in internal/migrations as
// numbered SQL. AutoMigrate cannot express a down migration and silently does
// nothing on a column rename, which turns a rename into data loss that no
// review catches.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	// Registers the pgx stdlib driver under the name "pgx". GORM is opened
	// over an existing *sql.DB rather than letting the driver open its own,
	// which is the only way to reach SetMaxOpenConns.
	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// Schema is the Postgres schema every control plane table lives in.
//
// Separate from the gateway's "private" schema on purpose. The two products
// can share a database, and a shared schema means a migration in one can
// collide with a table name in the other.
const Schema = "controlplane"

// ParseURI parses a Postgres connection URI and rejects anything the driver
// cannot use. It is the only place in this module that parses one.
//
// Two things it does that url.Parse alone does not.
//
// It checks the scheme. url.Parse accepts "postgress://host/db",
// "localhost:5432/db" and a libpq keyword string; all three then reach the
// driver and produce a message that never names the setting that was wrong.
//
// It unwraps the parse error before returning it. *url.Error stringifies as
// `parse "<the whole URL>": <cause>`, with no redaction, so a password
// containing an unescaped %, which password generators produce routinely,
// ends up verbatim in whatever aggregates stderr. net/http strips the
// password before building its url.Error; net/url does not. Only the cause
// crosses this boundary.
func ParseURI(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid postgres uri, reason=%v", errors.Unwrap(err))
	}
	switch u.Scheme {
	case "postgres", "postgresql":
	default:
		return nil, fmt.Errorf("postgres uri must use the postgres:// or postgresql:// scheme, got %q", u.Scheme)
	}
	return u, nil
}

// Open returns a connection pool for dsn. maxOpenConns of zero is unlimited.
func Open(dsn string, maxOpenConns int) (*gorm.DB, error) {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("unable to open connection with pgx driver, reason=%v", err)
	}
	if maxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(maxOpenConns)
	}

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: Schema + "."},
		Logger:         logger.Default.LogMode(logger.Silent),
		// TranslateError turns a Postgres unique violation into
		// gorm.ErrDuplicatedKey and a missing row into gorm.ErrRecordNotFound.
		// Those two are the error vocabulary for the whole module; no feature
		// package should define its own sentinel for either. Without this
		// every caller matches on driver error strings, which is how a
		// Postgres upgrade breaks error handling in code nobody touched.
		TranslateError: true,
	})
	if err != nil {
		// Close the pool we just opened. sql.Open is lazy so nothing is
		// connected yet, but the *sql.DB still owns a finalizer and a
		// goroutine once it is used, and leaking one per failed startup
		// attempt matters to a process that retries.
		_ = sqlDB.Close()
		return nil, fmt.Errorf("failed opening connection with database (gorm), reason=%v", err)
	}
	return db, nil
}

// Ping verifies the connection is usable, not merely configured.
//
// sql.Open never contacts the server, so a pool built with a wrong host
// opens cleanly and fails on first use. The readiness probe calls this so an
// unreachable database reads as "not ready" and traffic is shed, rather than
// surfacing as a 500 on whichever request arrives first.
func Ping(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed reading underlying sql handle, reason=%v", err)
	}
	return sqlDB.PingContext(ctx)
}

// Close drains the pool. Only the owner of the handle calls it.
func Close(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// Timestamps is the created/updated pair every table here carries.
//
// Declared explicitly rather than embedding gorm.Model, which would also
// bring an autoincrement uint primary key and a DeletedAt that turns every
// query into a soft-delete query. No table in this repository uses either.
type Timestamps struct {
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}
