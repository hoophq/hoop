package types

import (
	"fmt"

	pb "github.com/runopsio/hoop/common/proto"
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

func (c *APIContext) IsAdminUser() bool { return pb.IsInList(GroupAdmin, c.UserGroups) }

// SetName set the attribute name using from the Connection structure
func (p *PluginConnection) SetName() {
	if p != nil {
		p.Name = p.Connection.Name
	}
}
