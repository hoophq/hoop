package models

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
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

func CreateOrgGetOrganization(name string, licenseDataJSON []byte) (*Organization, bool, error) {
	org, err := GetOrganizationByNameOrID(name)
	switch err {
	case ErrNotFound:
		org, err = CreateOrganization(name, licenseDataJSON)
		return org, true, err
	case nil:
		return org, false, nil
	}
	return nil, false, err
}

func CreateOrganization(name string, licenseDataJSON []byte) (*Organization, error) {
	org := Organization{
		ID:          uuid.NewString(),
		Name:        name,
		LicenseData: licenseDataJSON,
		TotalUsers:  0,
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
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

	if err != nil {
		return &org, err
	}

	_, err = CreateDefaultRunbookConfiguration(DB, org.ID)
	if err != nil {
		log.Errorf("failed creating default runbook configuration, err=%v", err)
	}

	return &org, nil
}

func UpdateOrgLicense(orgID string, licenseDataJSON []byte) error {
	return DB.Table("private.orgs").
		Where("id = ?", orgID).
		Update("license_data", licenseDataJSON).
		Error
}

// DeleteOrganizationIfEmpty removes an org and its default scaffolding data (plugins, runbooks).
// It returns an error if the org still has users or if FK constraints prevent deletion
// (connections, sessions, agents, etc.).
func DeleteOrganizationIfEmpty(orgID string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var userCount int64
		if err := tx.Table("private.users").Where("org_id = ?", orgID).Count(&userCount).Error; err != nil {
			return err
		}
		if userCount > 0 {
			return fmt.Errorf("org %s still has %d users, cannot delete", orgID, userCount)
		}
		if err := tx.Exec(`DELETE FROM private.user_groups WHERE org_id = ?`, orgID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM private.runbook_rules WHERE org_id = ?`, orgID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM private.runbooks WHERE org_id = ?`, orgID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM private.plugins WHERE org_id = ?`, orgID).Error; err != nil {
			return err
		}
		return tx.Table("private.orgs").Where("id = ?", orgID).Delete(nil).Error
	})
}
