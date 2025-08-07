package models

import (
	"fmt"
	"time"

	"github.com/hoophq/hoop/common/memory"
)

var dbaccessStore = memory.New()

type DbAccess struct {
	ID             string    `gorm:"column:id"`
	OrgID          string    `gorm:"column:org_id"`
	UserID         string    `gorm:"column:user_id"`
	ConnectionName string    `gorm:"column:connection_name"`
	DbName         string    `gorm:"column:db_name"`
	DbHostname     string    `gorm:"column:hostname"`
	DbUsername     string    `gorm:"column:username"`
	DbPassword     string    `gorm:"column:password"`
	DbPort         string    `gorm:"column:port"`
	Status         string    `gorm:"column:status"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	ExpireAt       time.Time `gorm:"column:expire_at"`
}

func CreateDbAccess(db *DbAccess) (*DbAccess, error) {
	if dbaccessStore.Has(db.ID) {
		return nil, ErrAlreadyExists
	}
	dbaccessStore.Set(db.ID, db)
	return db, nil
}

func GetDbAccessByID(orgID, id string) (*DbAccess, error) {
	if !dbaccessStore.Has(id) {
		return nil, ErrNotFound
	}
	val := dbaccessStore.Get(id)
	db, ok := val.(*DbAccess)
	if !ok {
		return nil, fmt.Errorf("invalid type for DbAccess with ID %s", id)
	}
	return db, nil
}

func GetValidDbAccessBySecretKey(orgID, secretKey string) (*DbAccess, error) {
	for _, val := range dbaccessStore.List() {
		valDb, ok := val.(*DbAccess)
		if !ok {
			return nil, fmt.Errorf("invalid type in DbAccess store")
		}
		if valDb.DbUsername == secretKey {
			return valDb, nil
		}
	}
	return nil, ErrNotFound
}
