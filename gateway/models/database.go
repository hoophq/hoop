package models

import (
	"database/sql"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This makes the DB generally available to the application
// This is safe to access from multiple goroutines
var DB *gorm.DB

func InitDatabase() {
	log.Info("initializing database connection")

	dsn := appconfig.Get().PgURI()

	sqlDB, err := sql.Open("pgx", dsn)
	db, err := gorm.Open(postgres.New(postgres.Config{
		Conn: sqlDB,
	}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	err = db.Exec("SET search_path TO private").Error
	if err != nil {
		log.Fatalf("Failed to set schema: %v", err)
	}

	DB = db
}
