package models

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	pbsystem "github.com/hoophq/hoop/common/proto/system"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const tableDBRoleJobs = "private.dbrole_jobs"

type AWSDBRoleSpec struct {
	AccountArn    string           `json:"account_arn"`
	AccountUserID string           `json:"account_user_id"`
	Region        string           `json:"region"`
	DBArn         string           `json:"db_arn"`
	DBName        string           `json:"db_name"`
	DBEngine      string           `json:"db_engine"`
	Tags          []map[string]any `json:"db_tags"`
}

type DBRoleStatus struct {
	Phase   string               `json:"phase"`
	Message string               `json:"message"`
	Result  []DBRoleStatusResult `json:"result"`
}

type HookStatus struct {
	ExitCode         int    `json:"exit_code"`
	OutputBase64     string `json:"output"`
	ExecutionTimeSec int    `json:"execution_time_sec"`
}

type DBRoleStatusResultCredentialsInfo struct {
	SecretsManagerProvider string   `json:"secrets_manager_provider"`
	SecretID               string   `json:"secret_id"`
	SecretKeys             []string `json:"secret_keys"`
}

type DBRoleStatusResult struct {
	UserRole        string                            `json:"user_role"`
	CredentialsInfo DBRoleStatusResultCredentialsInfo `json:"credentials_info"`
	Status          string                            `json:"phase"`
	Message         string                            `json:"message"`
	CompletedAt     time.Time                         `json:"completed_at"`
}

type DBRole struct {
	OrgID         string         `gorm:"column:org_id"`
	ID            string         `gorm:"column:id"`
	CreatedAt     time.Time      `gorm:"column:created_at"`
	CompletedAt   *time.Time     `gorm:"column:completed_at"`
	StatusMap     map[string]any `gorm:"column:status;serializer:json"`
	HookStatusMap map[string]any `gorm:"column:hook_status;serializer:json"`
	SpecMap       map[string]any `gorm:"column:spec;serializer:json"` // Don't export it, having a lowercase it will serialize properly?

	Status     *DBRoleStatus  `gorm:"-"`
	HookStatus *HookStatus    `gorm:"-"`
	Spec       *AWSDBRoleSpec `gorm:"-"`
}

func CreateDBRoleJob(obj *DBRole) error {
	obj.SpecMap = dbRoleSpecToMap(obj.Spec)
	obj.StatusMap = dbRoleStatusToMap(obj.Status)
	obj.HookStatusMap = hookStatusToMap(obj.HookStatus)
	err := DB.Table(tableDBRoleJobs).Model(obj).Create(obj).Error
	if err == gorm.ErrDuplicatedKey {
		return ErrAlreadyExists
	}
	return err
}

func UpdateDBRoleJob(orgID string, completedAt *time.Time, resp *pbsystem.DBProvisionerResponse) (*DBRole, error) {
	job, err := GetDBRoleJobByID(orgID, resp.SID)
	if err != nil {
		return nil, err
	}

	// TODO: fix me
	specData, _ := json.Marshal(job.SpecMap)
	if err := json.Unmarshal(specData, &job.Spec); err != nil {
		return nil, fmt.Errorf("failed decoding spec data: %v", err)
	}

	var result []DBRoleStatusResult
	for _, r := range resp.Result {
		var cred pbsystem.DBCredentials
		if r.Credentials != nil {
			cred = *r.Credentials
		}
		result = append(result, DBRoleStatusResult{
			UserRole: cred.User,
			CredentialsInfo: DBRoleStatusResultCredentialsInfo{
				SecretsManagerProvider: string(cred.SecretsManagerProvider),
				SecretID:               cred.SecretID,
				SecretKeys:             cred.SecretKeys,
			},
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

	var hookStatusMap map[string]any
	if resp.RunbookHook != nil {
		hookStatusMap = map[string]any{
			"exit_code":          resp.RunbookHook.ExitCode,
			"output":             base64.StdEncoding.EncodeToString([]byte(resp.RunbookHook.Output)),
			"execution_time_sec": resp.RunbookHook.ExecutionTimeSec,
		}
	}

	// TODO: fix me
	var statusMap map[string]any
	statusData, _ := json.Marshal(status)
	if err := json.Unmarshal(statusData, &statusMap); err != nil {
		return nil, fmt.Errorf("failed decoding status data: %v", err)
	}

	err = DB.Table(tableDBRoleJobs).
		Model(job).
		Clauses(clause.Returning{}).
		Updates(DBRole{
			StatusMap:     statusMap,
			HookStatusMap: hookStatusMap,
			SpecMap:       dbRoleSpecToMap(job.Spec),
			CompletedAt:   completedAt,
		}).Where("org_id = ? AND id = ?", orgID, resp.SID).
		Error
	if err == gorm.ErrDuplicatedKey {
		return nil, ErrAlreadyExists
	}

	job.Status = status
	return job, nil
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
		if err := json.Unmarshal(statusData, &j.Status); err != nil {
			return nil, fmt.Errorf("failed decoding status data: %v", err)
		}

		hookStatus, _ := json.Marshal(j.HookStatusMap)
		if err := json.Unmarshal(hookStatus, &j.HookStatus); err != nil {
			return nil, fmt.Errorf("failed decoding hook status data: %v", err)
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

	// TODO: fix me
	hookStatus, _ := json.Marshal(job.HookStatusMap)
	if err := json.Unmarshal(hookStatus, &job.HookStatus); err != nil {
		return nil, fmt.Errorf("failed decoding hook status data: %v", err)
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
		"db_tags":     spec.Tags,
	}
}

func dbRoleStatusToMap(s *DBRoleStatus) (res map[string]any) {
	specData, _ := json.Marshal(s)
	_ = json.Unmarshal(specData, &res)
	return
}

func hookStatusToMap(s *HookStatus) (res map[string]any) {
	if s == nil {
		return nil
	}
	return map[string]any{
		"exit_code":          s.ExitCode,
		"output":             base64.StdEncoding.EncodeToString([]byte(s.OutputBase64)),
		"execution_time_sec": s.ExecutionTimeSec,
	}
}
