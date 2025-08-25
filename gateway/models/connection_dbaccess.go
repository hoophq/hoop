package models

import (
	"time"

	"github.com/hoophq/hoop/common/memory"
	"gorm.io/gorm"
)

var dbaccessStore = memory.New()

type DbAccess struct {
	ID             string    `gorm:"column:id"`
	OrgID          string    `gorm:"column:org_id"`
	UserSubject    string    `gorm:"column:user_subject"`
	ConnectionName string    `gorm:"column:connection_name"`
	SecretKeyHash  string    `gorm:"column:secret_key_hash"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	ExpireAt       time.Time `gorm:"column:expire_at"`
}

func CreateConnectionDbAccess(db *DbAccess) (*DbAccess, error) {
	return db, DB.Table("private.connection_dbaccess").Create(db).Error
}

// func CreateConnectionDbAccess(db *DbAccess) (*DbAccess, error) {
// 	if dbaccessStore.Has(db.ID) {
// 		return nil, ErrAlreadyExists
// 	}
// 	dbaccessStore.Set(db.ID, db)
// 	return db, nil
// }

func GetConnectionDbAccessByID(orgID, id string) (*DbAccess, error) {
	var resp DbAccess
	err := DB.Table("private.connection_dbaccess").
		Where("org_id = ? AND id = ?", orgID, id).
		First(&resp).
		Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &resp, err
}

func GetValidDbAccessBySecretKey(secretKeyHash string) (*DbAccess, error) {
	var resp DbAccess
	err := DB.Table("private.connection_dbaccess").
		Where("secret_key_hash = ?", secretKeyHash).
		First(&resp).
		Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &resp, err
}
