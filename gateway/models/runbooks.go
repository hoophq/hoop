package models

import (
	"slices"
	"time"

	"github.com/hoophq/hoop/gateway/storagev2/types"
)

type Runbooks struct {
	ID                string              `gorm:"column:id"`
	OrgID             string              `gorm:"column:org_id"`
	RepositoryConfigs []map[string]string `gorm:"column:repository_configs;serializer:json"`
	CreatedAt         time.Time           `gorm:"column:created_at"`
	UpdatedAt         time.Time           `gorm:"column:updated_at"`
}

type RunbookRules struct {
	ID                string    `gorm:"column:id"`
	OrgID             string    `gorm:"column:org_id"`
	Name              string    `gorm:"column:name"`
	Description       string    `gorm:"column:description"`
	UserGroups        []string  `gorm:"column:user_groups"`
	Connections       []string  `gorm:"column:connections"`
	AvailableRunbooks []string  `gorm:"column:available_runbooks"`
	CreatedAt         time.Time `gorm:"column:created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at"`
}

// GetUserAvailableRunbooks returns the list of available runbooks for a user based on their groups
// If there are no runbook rules defined for the organization, it returns nil, nil
// If there are runbook rules defined but none match the user's groups, it returns an empty list, nil
func GetUserAvailableRunbooks(orgId string, userGroups []string) ([]string, error) {
	if slices.Contains(userGroups, types.GroupAdmin) {
		return nil, nil
	}

	var runbookCount int64
	err := DB.
		Table("runbook_rules").
		Where("org_id = ?", orgId).
		Count(&runbookCount).Error
	if err != nil {
		return nil, err
	}
	if runbookCount == 0 {
		return nil, nil
	}

	var availableRunbooksFromRules [][]string
	err = DB.
		Table("runbook_rules").
		Select("available_runbooks").
		Where("org_id = ? AND (CARDINALITY(user_groups) = 0 OR user_groups && ?)", orgId, userGroups).
		Scan(&availableRunbooksFromRules).Error
	if err != nil {
		return nil, err
	}

	hasEmptyRunbooks := slices.ContainsFunc(availableRunbooksFromRules, func(runbooks []string) bool {
		return len(runbooks) == 0
	})

	if hasEmptyRunbooks {
		return nil, nil
	}

	var availableRunbooks []string
	for _, runbooks := range availableRunbooksFromRules {
		availableRunbooks = append(availableRunbooks, runbooks...)
	}

	availableRunbooks = slices.Compact(availableRunbooks)

	return availableRunbooks, nil
}

func IsUserAllowedToRunRunbook(orgId, connection, runbookPath string, userGroups []string) (bool, error) {
	if slices.Contains(userGroups, types.GroupAdmin) {
		return true, nil
	}

	var count int64
	err := DB.
		Table("runbook_rules").
		Where(`
		org_id = ? AND
		(CARDINALITY(user_groups) = 0 OR user_groups && ?) AND
		(CARDINALITY(connections) = 0 OR connections && ?) AND
		(CARDINALITY(available_runbooks) = 0 OR available_runbooks && ?)
		`, orgId, []string{connection}, userGroups, []string{runbookPath}).
		Count(&count).Error
	if err != nil {
		return false, err
	}

	return count > 0, nil
}
