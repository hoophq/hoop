package pgrest

var EmptyContext = NewOrgContext("")

type orgContext struct {
	orgID string
}

func (c *orgContext) GetOrgID() string      { return c.orgID }
func NewOrgContext(orgID string) OrgContext { return &orgContext{orgID: orgID} }
