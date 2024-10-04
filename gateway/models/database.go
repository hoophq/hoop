package models

import (
	"database/sql"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// This makes the DB generally available to the application
var DB *gorm.DB

func InitDatabase() {
	log.Info("initializing database connection")
	dsn := appconfig.Get().PgURI() + "&search_path=private"
	sqlDB, err := sql.Open("pgx", dsn)

	db, err := gorm.Open(postgres.New(postgres.Config{
		Conn: sqlDB,
	}), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	DB = db
}
