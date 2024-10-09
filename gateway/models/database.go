package models

import (
	"database/sql"
	"fmt"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

const defaultSchema = "private"

// This makes the DB generally available to the application
// This is safe to access from multiple goroutines
var DB *gorm.DB

func InitDatabaseConnection() error {
	log.Info("initializing database connection")

	dsn := appconfig.Get().PgURI()
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("unable to open connection with pgx driver, err=%v", err)
	}
	config := &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			TablePrefix: defaultSchema + ".",
		},
		Logger: logger.Default.LogMode(logger.Silent),
	}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), config)
	if err != nil {
		return fmt.Errorf("failed opening connection with database (gorm), err=%v", err)
	}
	DB = db
	return nil
}
