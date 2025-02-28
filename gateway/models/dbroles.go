package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const tableDBRoleJobs = "private.dbrole_jobs"

type AWSDBRoleSpec struct {
	AccountArn    string       `json:"account_arn"`
	AccountUserID string       `json:"account_user_id"`
	Region        string       `json:"region"`
	DBArn         string       `json:"db_arn"`
	DBName        string       `json:"db_name"`
	DBEngine      string       `json:"db_engine"`
	Roles         []DBRoleItem `json:"roles"`
}

type DBRoleItem struct {
	User        string            `json:"user"`
	Permissions []string          `json:"permissions"`
	Secrets     map[string]string `json:"secrets"`
	Error       *string           `json:"error"`
}

type DBRole struct {
	OrgID     string         `gorm:"column:org_id"`
	ID        string         `gorm:"column:id"`
	Status    string         `gorm:"column:status"`
	ErrorMsg  *string        `gorm:"column:error_message"`
	CreatedAt time.Time      `gorm:"column:created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at"`
	SpecMap   map[string]any `gorm:"column:spec;serializer:json"` // Don't export it, having a lowercase it will serialize properly?
	Spec      *AWSDBRoleSpec `gorm:"-"`
}

func CreateDBRoleJob(obj *DBRole) error {
	obj.SpecMap = dbRoleSpecToMap(obj.Spec)
	err := DB.Table(tableDBRoleJobs).Model(obj).Create(obj).Error
	if err == gorm.ErrDuplicatedKey {
		return ErrAlreadyExists
	}
	return err
}

func UpdateDBRoleJobSpec(orgID, jobID, status string, errMsg *string, roles ...DBRoleItem) error {
	job, err := GetDBRoleJobByID(orgID, jobID)
	if err != nil {
		return err
	}

	// TODO: fix that
	specData, _ := json.Marshal(job.SpecMap)
	if err := json.Unmarshal(specData, &job.Spec); err != nil {
		return fmt.Errorf("failed decoding spec data: %v", err)
	}
	job.Spec.Roles = roles
	job.ErrorMsg = errMsg

	// TODO: add master user and password to the env_vars

	err = DB.Table(tableDBRoleJobs).
		Model(job).
		Updates(DBRole{
			Status:  status,
			SpecMap: dbRoleSpecToMap(job.Spec),
		}).Where("org_id = ? AND id = ?", orgID, jobID).
		Error
	if err == gorm.ErrDuplicatedKey {
		return ErrAlreadyExists
	}
	// return err
	return nil
}

func ListDBRoleJobs(orgID string) ([]*DBRole, error) {
	var dbRoles []*DBRole
	err := DB.Table(tableDBRoleJobs).
		Where("org_id = ?", orgID).Find(&dbRoles).Error
	if err != nil {
		return nil, err
	}
	for _, j := range dbRoles {
		specData, _ := json.Marshal(j.SpecMap)
		if err := json.Unmarshal(specData, &j.Spec); err != nil {
			return nil, fmt.Errorf("failed decoding spec data: %v", err)
		}
	}
	return dbRoles, nil
}

func GetDBRoleJobByID(orgID, jobID string) (*DBRole, error) {
	var job DBRole
	if err := DB.Table(tableDBRoleJobs).Where("org_id = ? AND id = ?", orgID, jobID).
		First(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// TODO: fix me
	specData, _ := json.Marshal(job.SpecMap)
	if err := json.Unmarshal(specData, &job.Spec); err != nil {
		return nil, fmt.Errorf("failed decoding spec data: %v", err)
	}
	return &job, nil
}

func dbRoleSpecToMap(spec *AWSDBRoleSpec) map[string]any {
	roles := []map[string]any{}
	for _, role := range spec.Roles {
		roles = append(roles, map[string]any{
			"user":        role.User,
			"permissions": role.Permissions,
			"secrets":     role.Secrets,
		})
	}
	return map[string]any{
		"account_arn": spec.AccountArn,
		"user_id":     spec.AccountUserID,
		"db_arn":      spec.DBArn,
		"db_name":     spec.DBName,
		"db_engine":   spec.DBEngine,
		"roles":       roles,
	}
}
