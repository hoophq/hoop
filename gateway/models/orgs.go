package models

import (
	"encoding/json"
	"fmt"
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
	(SELECT count(*) FROM private.users u WHERE u.org_id = o.id) AS total_users
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

	return &org, DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Table("private.orgs").Create(&org).Error
		if err != nil {
			return err
		}
		// activate the default plugins when any organization is created
		for _, pluginName := range defaultPluginNames {
			err := tx.Exec(`
			INSERT INTO private.plugins (org_id, name)
			VALUES (?, ?)
			ON CONFLICT (org_id, name) DO NOTHING`, org.ID, pluginName).
				Error
			if err != nil {
				return fmt.Errorf("failed to create default plugin %s for org %s, reason: %v",
					pluginName, org.ID, err)
			}
		}
		return nil
	})
}

func UpdateOrgLicense(orgID string, licenseDataJSON []byte) error {
	return DB.Table("private.orgs").
		Where("id = ?", orgID).
		Update("license_data", licenseDataJSON).
		Error
}
