package models

import (
	"fmt"

	"gorm.io/gorm"
)

type ServerMiscConfig struct {
	GrpcServerURL         *string                `gorm:"column:grpc_server_url"`
	PostgresServerConfig  *PostgresServerConfig  `gorm:"column:postgres_server_config;serializer:json"`
	SSHServerConfig       *SSHServerConfig       `gorm:"column:ssh_server_config;serializer:json"`
	RDPServerConfig       *RDPServerConfig       `gorm:"column:rdp_server_config;serializer:json"`
	HttpProxyServerConfig *HttpProxyServerConfig `gorm:"column:http_proxy_server_config;serializer:json"`
}

type HttpProxyServerConfig struct {
	ListenAddress string `json:"listen_address"`
}
type RDPServerConfig struct {
	ListenAddress string `json:"listen_address"`
}

type PostgresServerConfig struct {
	ListenAddress string `json:"listen_address"`
}

// SSHUserMapping configures how a certificate attribute is matched against a
// Hoop user attribute to authenticate certificate-based SSH connections.
type SSHUserMapping struct {
	CertAttribute string `json:"cert_attr"` // "principal" or "key_id"
	UserAttribute string `json:"user_attr"` // "email", "subject", or "user_id"
}

type SSHServerConfig struct {
	ListenAddress string          `json:"listen_address"`
	HostsKey      string          `json:"hosts_key"`
	TrustedCAs    []string        `json:"trusted_cas,omitempty"`
	UserMapping   *SSHUserMapping `json:"user_mapping,omitempty"`
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
				"grpc_server_url":          newObj.GrpcServerURL,
				"postgres_server_config":   newObj.PostgresServerConfig,
				"ssh_server_config":        newObj.SSHServerConfig,
				"rdp_server_config":        newObj.RDPServerConfig,
				"http_proxy_server_config": newObj.HttpProxyServerConfig,
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
