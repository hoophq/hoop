package pbsys

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
)

type DBProvisionerRequest struct {
	OrgID            string `json:"org_id"`
	SID              string `json:"sid"`
	ResourceID       string `json:"resource_id"`
	DatabaseHostname string `json:"hostname"`
	DatabasePort     string `json:"port"`
	MasterUsername   string `json:"master_user"`
	MasterPassword   string `json:"master_password"`
	DatabaseType     string `json:"database_type"`
}

type DBCredentials struct {
	Host     string            `json:"host"`
	Port     string            `json:"port"`
	User     string            `json:"user"`
	Password string            `json:"password"`
	Options  map[string]string `json:"options"`
}

type Result struct {
	Credentials    *DBCredentials `json:"db_credentials"`
	RoleSuffixName string         `json:"role_suffix_name"`
	Status         string         `json:"status"`
	Message        string         `json:"message"`
	CompletedAt    time.Time      `json:"completed_at"`
}

type DBProvisionerStatus struct {
	Phase   string   `json:"phase"`
	Status  string   `json:"status"`
	Message string   `json:"message"`
	Result  []Result `json:"result"`
}

type DBProvisionerResponse struct {
	SID     string   `json:"sid"`
	Status  string   `json:"status"`
	Message string   `json:"message"`
	Result  []Result `json:"result"`
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
	return fmt.Sprintf("status=%v, message=%v", r.Status, r.Message)
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
		Result:  []Result{},
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
