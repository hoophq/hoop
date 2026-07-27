package models

import (
	"database/sql"
	"fmt"

	"github.com/hoophq/hoop/common/log"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

const defaultSchema = "private"

// This makes the DB generally available to the application
// This is safe to access from multiple goroutines
var DB *gorm.DB

// InitDatabaseConnection opens the global database connection pool against
// dsn. maxOpenConns caps the pool size; zero keeps the driver default. The
// embedded PGlite backend serves a single wire-protocol session at a time,
// so callers using it must pass maxOpenConns=1.
func InitDatabaseConnection(dsn string, maxOpenConns int) error {
	log.Debug("initializing database connection")

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("unable to open connection with pgx driver, err=%v", err)
	}
	if maxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(maxOpenConns)
	}
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
