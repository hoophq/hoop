package pgrest

var EmptyContext = NewOrgContext("")

type orgContext struct {
	orgID string
}

type auditContext struct {
	orgID     string
	eventName string
	userEmail string
	metadata  map[string]any
}

func (c *orgContext) GetOrgID() string { return c.orgID }

func NewOrgContext(orgID string) OrgContext { return &orgContext{orgID: orgID} }
func NewAuditContext(orgID, eventName, userEmail string) *auditContext {
	return &auditContext{
		orgID:     orgID,
		eventName: eventName,
		userEmail: userEmail,
		metadata:  nil,
	}
}
func (c *auditContext) GetOrgID() string            { return c.orgID }
func (c *auditContext) GetUserEmail() string        { return c.userEmail }
func (c *auditContext) GetEventName() string        { return c.eventName }
func (c *auditContext) GetMetadata() map[string]any { return c.metadata }
func (c *auditContext) WithMetadata(v map[string]any) *auditContext {
	c.metadata = v
	return c
}
