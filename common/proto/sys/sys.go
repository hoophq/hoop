package pbsys

import (
	"encoding/json"
	"fmt"

	"github.com/aws/smithy-go/ptr"
)

const (
	ProvisionDBRolesRequest  string = "SysProvisionDBRolesRequest"
	ProvisionDBRolesResponse string = "SysProvisionDBRolesResponse"
)

type DBProvisionerRequest struct {
	OrgID          string `json:"org_id"`
	SID            string `json:"sid"`
	EndpointAddr   string `json:"endpoint_address"`
	MasterUsername string `json:"master_user"`
	MasterPassword string `json:"master_password"`
	DatabaseType   string `json:"database_type"`
}

type DBProvisionerResponse struct {
	SID          string  `json:"sid"`
	ErrorMessage *string `json:"error_message"`
}

func (r *DBProvisionerResponse) Error() string {
	if r.ErrorMessage != nil {
		return *r.ErrorMessage
	}
	return ""
}

func NewDbProvisionerRequest(req *DBProvisionerRequest) ([]byte, string, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, "", err
	}
	return payload, ProvisionDBRolesRequest, nil
}

func NewDbProvisionerResponse(resp *DBProvisionerResponse) ([]byte, string, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, "", err
	}
	return payload, ProvisionDBRolesResponse, nil
}

func NewError(sid, format string, a ...any) *DBProvisionerResponse {
	return &DBProvisionerResponse{
		SID:          sid,
		ErrorMessage: ptr.String(fmt.Sprintf(format, a...)),
	}
}
