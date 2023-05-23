package types

import "fmt"

// Validate if the organization id and user id is set
func (c *UserContext) Validate() error {
	if c.OrgID == "" || c.UserID == "" {
		return fmt.Errorf("missing required user context")
	}
	return nil
}
