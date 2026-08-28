// Package database owns the connection pool and nothing else. Each feature
// package owns its own tables, types and queries, taking *gorm.DB as a
// parameter and propagating gorm.ErrRecordNotFound so callers can tell
// "absent" from "broken".
//
// No AutoMigrate: schema lives in internal/migrations as numbered SQL.
// AutoMigrate cannot express a down migration and silently ignores column
// renames, turning a rename into data loss.
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
// Kept separate from the gateway's "private" schema so the two products can
// share a database without migration collisions.
const Schema = "controlplane"

// ParseURI parses a Postgres connection URI; the only URI parser in this
// module. Beyond url.Parse it checks the scheme (so a bad value fails here,
// naming the setting, not in the driver) and unwraps the parse error, since
// *url.Error embeds the whole URL — password included — unredacted.
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
		// TranslateError maps unique violations to gorm.ErrDuplicatedKey and
		// missing rows to gorm.ErrRecordNotFound — the error vocabulary for
		// the whole module; never match on driver error strings.
		TranslateError: true,
	})
	if err != nil {
		// sql.Open is lazy, but a used *sql.DB owns a finalizer and a
		// goroutine; leaking one per failed startup matters when retrying.
		_ = sqlDB.Close()
		return nil, fmt.Errorf("failed opening connection with database (gorm), reason=%v", err)
	}
	return db, nil
}

// Ping verifies the connection is usable, not merely configured: sql.Open
// never contacts the server. The readiness probe uses this so an unreachable
// database reads as "not ready" instead of a 500 on the first request.
func Ping(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed reading underlying sql handle, reason=%v", err)
	}
	return sqlDB.PingContext(ctx)
}

// Pinger adapts a pool to the single-method readiness check api wants,
// keeping gorm out of api's imports and letting a fake fail on demand.
type Pinger struct{ db *gorm.DB }

// NewPinger wraps db.
func NewPinger(db *gorm.DB) Pinger { return Pinger{db: db} }

// Ping verifies the connection is usable.
func (p Pinger) Ping(ctx context.Context) error { return Ping(ctx, p.db) }

// Close drains the pool. Only the owner of the handle calls it.
func Close(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// Timestamps is the created/updated pair every table here carries.
// Declared instead of embedding gorm.Model, which would add an autoincrement
// key and soft-delete semantics nothing here uses.
type Timestamps struct {
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}
