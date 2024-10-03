package models

import (
	"database/sql"
	"fmt"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// This makes the DB generally available to the application
var DB *gorm.DB

func InitDatabase() {
	log.Info("initializing database connection")
	dsn := appconfig.Get().PgURI()
	sqlDB, err := sql.Open("pgx", dsn)
	db, err := gorm.Open(postgres.New(postgres.Config{
		Conn: sqlDB,
	}), &gorm.Config{})

	if err != nil {
		panic("failed to connect database")
	}
	// db.AutoMigrate(&User{}, &Org{}, &Sessions{}, ...)
	db.AutoMigrate(&User{}, &UserGroup{})
	var users []User

	result := db.Find(&users)
	fmt.Printf("result: %+v\n", result)
	fmt.Printf("users: %+v\n", users)
	DB = db
}
