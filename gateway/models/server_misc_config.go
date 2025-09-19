package models

import (
	"fmt"

	"gorm.io/gorm"
)

type ServerMiscConfig struct {
	ProductAnalytics     *string               `gorm:"column:product_analytics"`
	GrpcServerURL        *string               `gorm:"column:grpc_server_url"`
	PostgresServerConfig *PostgresServerConfig `gorm:"column:postgres_server_config;serializer:json"`
	SSHServerConfig      *SSHServerConfig      `gorm:"column:ssh_server_config;serializer:json"`
}

type PostgresServerConfig struct {
	ListenAddress string `json:"listen_address"`
}

type SSHServerConfig struct {
	ListenAddress string `json:"listen_address"`
	HostsKey      string `json:"hosts_key"`
}

func GetServerMiscConfig() (*ServerMiscConfig, error) {
	var config ServerMiscConfig
	err := DB.Table("private.serverconfig").First(&config).Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &config, err
}

func UpsertServerMiscConfig(newObj *ServerMiscConfig) (*ServerMiscConfig, error) {
	_, err := GetServerMiscConfig()
	switch err {
	case ErrNotFound:
		return newObj, DB.Table("private.serverconfig").
			Create(newObj).
			Error
	case nil:
		res := DB.Table("private.serverconfig").
			Where("1=1").
			Updates(map[string]any{
				"product_analytics":      newObj.ProductAnalytics,
				"grpc_server_url":        newObj.GrpcServerURL,
				"postgres_server_config": newObj.PostgresServerConfig,
				"ssh_server_config":      newObj.SSHServerConfig,
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
	res := DB.Exec(`
	INSERT INTO private.serverconfig (shared_signing_key)
	SELECT ?
	WHERE NOT EXISTS (
		SELECT 1
		FROM private.serverconfig
		WHERE shared_signing_key IS NOT NULL
	)`, encB64Key)
	if res.RowsAffected == 0 {
		return ErrAlreadyExists
	}
	return nil
}
