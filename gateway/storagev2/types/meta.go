package types

import (
	"encoding/json"
	"fmt"

	"github.com/hoophq/hoop/common/license"
	pb "github.com/hoophq/hoop/common/proto"
)

// Validate if the organization id and user id is set
func (c *APIContext) Validate() error {
	if c.OrgID == "" || c.UserID == "" {
		return fmt.Errorf("missing required user context")
	}
	return nil
}

func (c *ConnectionInfo) Validate() error {
	if c.Name == "" || c.AgentID == "" ||
		c.ID == "" || c.Type == "" {
		return fmt.Errorf("missing required connection attributes")
	}
	return nil
}

func (c *APIContext) GetLicenseType() string {
	if c == nil || c.OrgLicenseData == nil || len(*c.OrgLicenseData) == 0 {
		return license.OSSType
	}
	var l license.License
	if err := json.Unmarshal(*c.OrgLicenseData, &l); err != nil {
		return license.OSSType
	}
	return l.Payload.Type
}

func (c *APIContext) IsAdminUser() bool   { return pb.IsInList(GroupAdmin, c.UserGroups) }
func (c *APIContext) IsAuditorUser() bool { return pb.IsInList(GroupAuditor, c.UserGroups) }
func (c *APIContext) IsAuditorOrAdminUser() bool {
	if c.IsAdminUser() || c.IsAuditorUser() {
		return true
	}
	return false
}

// SetName set the attribute name using from the Connection structure
func (p *PluginConnection) SetName() {
	if p != nil {
		p.Name = p.Connection.Name
	}
}
