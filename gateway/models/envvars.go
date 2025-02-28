package models

import (
	"encoding/base64"
	"errors"
	"time"

	"gorm.io/gorm"
)

const tableEnvVars = "private.env_vars"

type EnvVar struct {
	OrgID     string            `gorm:"column:org_id"`
	ID        string            `gorm:"column:id"`
	Envs      map[string]string `gorm:"column:envs;serializer:json"`
	UpdatedAt time.Time         `gorm:"column:updated_at"`
}

func (e *EnvVar) GetEnv(key string) (v string) {
	key = "envvar:" + key
	if len(e.Envs) == 0 {
		return
	}
	if encVal := e.Envs[key]; encVal != "" {
		val, _ := base64.StdEncoding.DecodeString(encVal)
		if len(val) > 0 {
			return string(val)
		}
	}
	return
}

func (e *EnvVar) SetEnv(key, val string) {
	key = "envvar:" + key
	val = base64.StdEncoding.EncodeToString([]byte(val))
	if len(e.Envs) == 0 {
		e.Envs = map[string]string{key: val}
		return
	}
	e.Envs[key] = val
}

func (e *EnvVar) HasKey(key string) (v bool) {
	key = "envvar:" + key
	if len(e.Envs) == 0 {
		return
	}
	val, ok := e.Envs[key]
	return ok && len(val) > 0
}

func GetEnvVarByID(orgID, id string) (*EnvVar, error) {
	var env EnvVar
	if err := DB.Table(tableEnvVars).Where("org_id = ? AND id = ?", orgID, id).
		First(&env).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &env, nil
}

func UpsertEnvVar(env *EnvVar) error {
	return DB.Table(tableEnvVars).
		Model(env).
		Save(env).Error
}
