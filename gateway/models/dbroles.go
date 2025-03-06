package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	pbsys "github.com/hoophq/hoop/common/proto/sys"
	"gorm.io/gorm"
)

const tableDBRoleJobs = "private.dbrole_jobs"

type AWSDBRoleSpec struct {
	AccountArn    string `json:"account_arn"`
	AccountUserID string `json:"account_user_id"`
	Region        string `json:"region"`
	DBArn         string `json:"db_arn"`
	DBName        string `json:"db_name"`
	DBEngine      string `json:"db_engine"`
}

type DBRoleStatus struct {
	Phase   string               `json:"phase"`
	Message string               `json:"message"`
	Result  []DBRoleStatusResult `json:"result"`
}

type DBRoleStatusResult struct {
	UserRole    string    `json:"user_role"`
	Status      string    `json:"phase"`
	Message     string    `json:"message"`
	CompletedAt time.Time `json:"completed_at"`
}

type DBRoleItem struct {
	User        string            `json:"user"`
	Permissions []string          `json:"permissions"`
	Secrets     map[string]string `json:"secrets"`
	Error       *string           `json:"error"`
}

type DBRole struct {
	OrgID       string         `gorm:"column:org_id"`
	ID          string         `gorm:"column:id"`
	CreatedAt   time.Time      `gorm:"column:created_at"`
	CompletedAt *time.Time     `gorm:"column:completed_at"`
	StatusMap   map[string]any `gorm:"column:status;serializer:json"`
	SpecMap     map[string]any `gorm:"column:spec;serializer:json"` // Don't export it, having a lowercase it will serialize properly?

	Status *DBRoleStatus  `gorm:"-"`
	Spec   *AWSDBRoleSpec `gorm:"-"`
}

func CreateDBRoleJob(obj *DBRole) error {
	obj.SpecMap = dbRoleSpecToMap(obj.Spec)
	obj.StatusMap = dbRoleStatusToMap(obj.Status)
	err := DB.Table(tableDBRoleJobs).Model(obj).Create(obj).Error
	if err == gorm.ErrDuplicatedKey {
		return ErrAlreadyExists
	}
	return err
}

func UpdateDBRoleJob(orgID string, completedAt *time.Time, resp *pbsys.DBProvisionerResponse) error {
	job, err := GetDBRoleJobByID(orgID, resp.SID)
	if err != nil {
		return err
	}

	// TODO: fix me
	specData, _ := json.Marshal(job.SpecMap)
	if err := json.Unmarshal(specData, &job.Spec); err != nil {
		return fmt.Errorf("failed decoding spec data: %v", err)
	}

	var result []DBRoleStatusResult
	for _, r := range resp.Result {
		var userRole string
		if r.Credentials != nil {
			userRole = r.Credentials.User
		}
		result = append(result, DBRoleStatusResult{
			UserRole:    userRole,
			Status:      r.Status,
			Message:     r.Message,
			CompletedAt: r.CompletedAt,
		})
	}

	status := &DBRoleStatus{
		Phase:   resp.Status,
		Message: resp.Message,
		Result:  result,
	}

	// TODO: fix me
	var statusMap map[string]any
	statusData, _ := json.Marshal(status)
	if err := json.Unmarshal(statusData, &statusMap); err != nil {
		return fmt.Errorf("failed decoding status data: %v", err)
	}

	err = DB.Table(tableDBRoleJobs).
		Model(job).
		Updates(DBRole{
			StatusMap:   statusMap,
			SpecMap:     dbRoleSpecToMap(job.Spec),
			CompletedAt: completedAt,
		}).Where("org_id = ? AND id = ?", orgID, resp.SID).
		Error
	if err == gorm.ErrDuplicatedKey {
		return ErrAlreadyExists
	}
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

		statusData, _ := json.Marshal(j.StatusMap)
		if err := json.Unmarshal(statusData, &j.Spec); err != nil {
			return nil, fmt.Errorf("failed decoding status data: %v", err)
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

	// TODO: fix me
	statusData, _ := json.Marshal(job.StatusMap)
	if err := json.Unmarshal(statusData, &job.Status); err != nil {
		return nil, fmt.Errorf("failed decoding status data: %v", err)
	}

	return &job, nil
}

func dbRoleSpecToMap(spec *AWSDBRoleSpec) map[string]any {
	return map[string]any{
		"account_arn": spec.AccountArn,
		"user_id":     spec.AccountUserID,
		"db_arn":      spec.DBArn,
		"db_name":     spec.DBName,
		"db_engine":   spec.DBEngine,
	}
}

func dbRoleStatusToMap(s *DBRoleStatus) (res map[string]any) {
	specData, _ := json.Marshal(s)
	_ = json.Unmarshal(specData, &res)
	return
}
