package models

import (
	"database/sql"
	"fmt"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// This makes the DB generally available to the application
var DB *gorm.DB

func InitDatabase() {
	log.Info("initializing database connection")
	dsn := appconfig.Get().PgURI()
	// TODO: connect directly to the private
	sqlDB, err := sql.Open("pgx", dsn)
	// TODO: manage the connection pool for network problems
	db, err := gorm.Open(postgres.New(postgres.Config{
		Conn: sqlDB,
	}), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			TablePrefix: "private.", // this connects to the private schema when accessing the db
		},
	})

	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	// db.AutoMigrate(&User{}, &Org{}, &Sessions{}, ...)
	// db.AutoMigrate(&User{}, &UserGroup{})
	var users []User

	result := db.Find(&users)
	fmt.Printf("result: %+v\n", result)
	fmt.Printf("users: %+v\n", users)
	DB = db
}
