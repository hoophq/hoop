package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Organization struct {
	ID          string          `gorm:"column:id"`
	Name        string          `gorm:"column:name"`
	CreatedAt   time.Time       `gorm:"column:created_at"`
	LicenseData json.RawMessage `gorm:"column:license_data"`
	TotalUsers  int64           `gorm:"column:total_users;->"`
}

func ListAllOrganizations() ([]Organization, error) {
	var orgs []Organization
	return orgs,
		DB.Table("private.orgs").Find(&orgs).Error
}

func GetOrganizationByNameOrID(nameOrID string) (*Organization, error) {
	var org Organization
	err := DB.Raw(`
	SELECT o.id, o.name, license_data,
	(SELECT count(*) FROM users u WHERE u.org_id = o.id) AS total_users
	FROM private.orgs o
	WHERE (o.id::TEXT = ? OR o.name = ?)`, nameOrID, nameOrID).
		First(&org).
		Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &org, err
}

func CreateOrgGetOrganization(name string, licenseDataJSON []byte) (*Organization, error) {
	org, err := GetOrganizationByNameOrID(name)
	switch err {
	case ErrNotFound:
		return CreateOrganization(name, licenseDataJSON)
	case nil:
		return org, nil
	}
	return nil, err
}

func CreateOrganization(name string, licenseDataJSON []byte) (*Organization, error) {
	org := Organization{
		ID:          uuid.NewString(),
		Name:        name,
		LicenseData: licenseDataJSON,
		TotalUsers:  0,
	}
	return &org, DB.Table("private.orgs").
		Create(&org).
		Error
}

func UpdateOrgLicense(orgID string, licenseDataJSON []byte) error {
	return DB.Table("private.orgs").
		Where("id = ?", orgID).
		Update("license_data", licenseDataJSON).
		Error
}
