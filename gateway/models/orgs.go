package models

import (
	"encoding/json"
	"time"
)

const tableOrgs = "private.orgs"

type Organization struct {
	ID          string          `gorm:"id"`
	Name        string          `gorm:"name"`
	CreatedAt   time.Time       `gorm:"created_at"`
	LicenseData json.RawMessage `gorm:"column:license_data"`
}

func ListAllOrganizations() ([]Organization, error) {
	var orgs []Organization
	return orgs,
		DB.Table(tableOrgs).Find(&orgs).Error
}
