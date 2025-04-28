package pbsystem

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	ProvisionDBRolesRequest  string = "SysProvisionDBRolesRequest"
	ProvisionDBRolesResponse string = "SysProvisionDBRolesResponse"

	StatusRunningType   string = "running"
	StatusCompletedType string = "completed"
	StatusFailedType    string = "failed"

	MessageCompleted            string = "All user roles have been successfully provisioned"
	MessageOneOrMoreRolesFailed string = "One or more user roles failed to be provisioned"
	MessageVaultSaveError       string = "One or more user roles could not be saved to the Vault key-value store"
)

type VaultProvider struct {
	SecretID string `json:"secret_id"`
}

type ExecHook struct {
	Command   []string `json:"command"`
	InputFile string   `json:"input_file"`
}

type DBProvisionerRequest struct {
	OrgID            string           `json:"org_id"`
	SID              string           `json:"sid"`
	ResourceID       string           `json:"resource_id"`
	DatabaseHostname string           `json:"hostname"`
	DatabasePort     string           `json:"port"`
	MasterUsername   string           `json:"master_user"`
	MasterPassword   string           `json:"master_password"`
	DatabaseType     string           `json:"database_type"`
	DatabaseTags     []map[string]any `json:"database_tags"`

	Vault    *VaultProvider `json:"vault_provider"`
	ExecHook *ExecHook      `json:"exec_hook"`
}

type SecretsManagerProviderType string

const (
	SecretsManagerProviderDatabase SecretsManagerProviderType = "database"
	SecretsManagerProviderVault    SecretsManagerProviderType = "vault"
)

type DBCredentials struct {
	Host                   string                     `json:"host"`
	Port                   string                     `json:"port"`
	User                   string                     `json:"user"`
	Password               string                     `json:"password"`
	DefaultDatabase        string                     `json:"default_database"`
	Options                map[string]string          `json:"options"`
	SecretsManagerProvider SecretsManagerProviderType `json:"secrets_manager_provider"`
	SecretID               string                     `json:"secret_id"`
	SecretKeys             []string                   `json:"secret_keys"`
}

type Result struct {
	Credentials    *DBCredentials `json:"db_credentials"`
	RoleSuffixName string         `json:"role_suffix_name"`
	Status         string         `json:"status"`
	Message        string         `json:"message"`
	CompletedAt    time.Time      `json:"completed_at"`
}

type RunbookHook struct {
	ExitCode         int    `json:"exit_code"`
	Output           string `json:"output"`
	ExecutionTimeSec int    `json:"execution_time_sec"`
}

type DBProvisionerStatus struct {
	Phase   string   `json:"phase"`
	Status  string   `json:"status"`
	Message string   `json:"message"`
	Result  []Result `json:"result"`
}

type DBProvisionerResponse struct {
	SID         string       `json:"sid"`
	Status      string       `json:"status"`
	Message     string       `json:"message"`
	Result      []*Result    `json:"result"`
	RunbookHook *RunbookHook `json:"runbook_hook,omitempty"`
}

func (r *DBProvisionerRequest) Address() string {
	return fmt.Sprintf("%s:%s", r.DatabaseHostname, r.Port())
}

func (r *DBProvisionerRequest) Port() (v string) {
	if r.DatabasePort != "" {
		return r.DatabasePort
	}
	switch r.DatabaseType {
	case "postgres":
		v = "5432"
	case "mysql":
		v = "3306"
	case "sqlserver-ee", "sqlserver-se", "sqlserver-ex", "sqlserver-web":
		v = "1443"
	case "mongodb-atlas":
		v = "27017"
	}
	return
}

func (r *DBProvisionerResponse) String() string {
	var rolesResultErr []string
	for _, r := range r.Result {
		if r.Message != "" {
			rolesResultErr = append(rolesResultErr, r.Message)
		}
	}
	return fmt.Sprintf("status=%v, message=%v, roles-message=%v", r.Status, r.Message, rolesResultErr)
}

func (r *DBProvisionerResponse) Encode() ([]byte, string, error) {
	payload, err := json.Marshal(r)
	if err != nil {
		return nil, "", err
	}

	return payload, ProvisionDBRolesResponse, nil
}

func NewDbProvisionerRequest(req *DBProvisionerRequest) ([]byte, string, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, "", err
	}
	return payload, ProvisionDBRolesRequest, nil
}

func NewDbProvisionerResponse(sid, status, message string) *DBProvisionerResponse {
	return &DBProvisionerResponse{
		SID:     sid,
		Status:  status,
		Message: message,
		Result:  []*Result{},
	}
}

func NewError(sid, format string, a ...any) *DBProvisionerResponse {
	return &DBProvisionerResponse{
		SID:     sid,
		Status:  StatusFailedType,
		Message: fmt.Sprintf(format, a...),
	}
}

func NewResultError(format string, a ...any) *Result {
	return &Result{
		Message:     fmt.Sprintf(format, a...),
		Status:      StatusFailedType,
		CompletedAt: time.Now().UTC(),
	}
}
