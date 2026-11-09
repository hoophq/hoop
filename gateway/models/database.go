package models

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/hoophq/hoop/common/log"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

const defaultSchema = "private"

// defaultMaxIdleConns is how many connections the pool keeps when the open
// count is unbounded. database/sql keeps 2 by default, so every request past
// two in flight opened a connection and closed it after: a TLS handshake,
// a backend fork and an empty statement cache, per request. A sidecar fleet
// handshaking every minute hit it on every overlap (5,000 sidecars: ~85
// handshakes/s, 8 statements each).
const defaultMaxIdleConns = 25

// connMaxIdleTime closes a connection a burst opened once it sits unused
// that long, so a quiet gateway does not hold its peak forever.
const connMaxIdleTime = 5 * time.Minute

// This makes the DB generally available to the application
// This is safe to access from multiple goroutines
var DB *gorm.DB

// InitDatabaseConnection opens the global database connection pool against
// dsn. maxOpenConns caps the pool size, and idle connections are kept up to
// that cap; zero leaves the open count unbounded and keeps
// defaultMaxIdleConns idle. The embedded PGlite backend serves a single
// wire-protocol session at a time, so callers using it must pass
// maxOpenConns=1.
func InitDatabaseConnection(dsn string, maxOpenConns int) error {
	log.Debug("initializing database connection")

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("unable to open connection with pgx driver, err=%v", err)
	}
	maxIdleConns := defaultMaxIdleConns
	if maxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(maxOpenConns)
		maxIdleConns = maxOpenConns
	}
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxIdleTime(connMaxIdleTime)
	config := &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			TablePrefix: defaultSchema + ".",
		},
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), config)
	if err != nil {
		return fmt.Errorf("failed opening connection with database (gorm), err=%v", err)
	}
	DB = db
	return nil
}
