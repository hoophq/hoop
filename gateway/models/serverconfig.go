package models

import (
	"fmt"

	"gorm.io/gorm"
)

type ServerConfig struct {
	ProductAnalytics      string `gorm:"column:product_analytics"`
	WebappUsersManagement string `gorm:"column:webapp_users_management"`
	GrpcServerURL         string `gorm:"column:grpc_server_url"`
	SharedSigningKey      string `gorm:"column:shared_signing_key;->"`
}

func GetServerConfig() (*ServerConfig, error) {
	var config ServerConfig
	err := DB.Table("private.serverconfig").First(&config).Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &config, err
}

func UpsertServerConfig(newObj *ServerConfig) (*ServerConfig, error) {
	_, err := GetServerConfig()
	switch err {
	case ErrNotFound:
		return newObj, DB.Table("private.serverconfig").
			Create(newObj).
			Error
	case nil:
		res := DB.Table("private.serverconfig").
			Where("1=1").
			Updates(map[string]any{
				"product_analytics":       newObj.ProductAnalytics,
				"webapp_users_management": newObj.WebappUsersManagement,
				"grpc_server_url":         newObj.GrpcServerURL,
			})
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected == 0 {
			return nil, fmt.Errorf("unable to update server config, no changes detected")
		}
		return newObj, nil
	}
	return nil, fmt.Errorf("failed to get server config, reason=%v", err)
}

func CreateServerSharedSigningKey(encB64Key string) error {
	return DB.Exec(`INSERT INTO private.serverconfig (shared_signing_key) VALUES (?)`, encB64Key).
		Error
}
