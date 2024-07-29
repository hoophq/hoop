package pgserviceaccounts

import (
	"net/url"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/pgrest"
)

type serviceAccount struct{}

func New() *serviceAccount { return &serviceAccount{} }

func (s *serviceAccount) FetchAll(ctx pgrest.OrgContext) ([]openapi.ServiceAccount, error) {
	items := []pgrest.ServiceAccount{}
	err := pgrest.New("/serviceaccounts?select=id,org_id,subject,name,status,created_at,updated_at,groups&org_id=eq.%s", ctx.GetOrgID()).
		FetchAll().
		DecodeInto(&items)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	var result []openapi.ServiceAccount
	for _, sa := range items {
		result = append(result, openapi.ServiceAccount{
			ID:      sa.ID,
			OrgID:   sa.OrgID,
			Subject: sa.Subject,
			Name:    sa.Name,
			Status:  openapi.ServiceAccountStatusType(sa.Status),
			Groups:  sa.Groups,
		})
	}
	return result, nil
}

func (s *serviceAccount) FetchOne(ctx pgrest.OrgContext, id string) (*openapi.ServiceAccount, error) {
	var sa pgrest.ServiceAccount
	err := pgrest.New("/serviceaccounts?select=id,org_id,subject,name,status,created_at,updated_at,groups&org_id=eq.%s&id=eq.%s",
		ctx.GetOrgID(), url.QueryEscape(id)).
		FetchOne().
		DecodeInto(&sa)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &openapi.ServiceAccount{
		ID:      sa.ID,
		OrgID:   sa.OrgID,
		Subject: sa.Subject,
		Name:    sa.Name,
		Status:  openapi.ServiceAccountStatusType(sa.Status),
		Groups:  sa.Groups,
	}, nil
}

func (s *serviceAccount) Upsert(ctx pgrest.OrgContext, req *openapi.ServiceAccount) (*openapi.ServiceAccount, error) {
	sa := pgrest.ServiceAccount{}
	err := pgrest.New("/rpc/update_serviceaccounts?select=id,org_id,subject,name,status,created_at,updated_at,groups&org_id=eq.%s", ctx.GetOrgID()).
		Create(map[string]any{
			"id":      req.ID,
			"org_id":  req.OrgID,
			"subject": req.Subject,
			"name":    req.Name,
			"status":  req.Status,
			"groups":  req.Groups,
		}).DecodeInto(&sa)
	if err != nil {
		return nil, err
	}
	return &openapi.ServiceAccount{
		ID:      sa.ID,
		OrgID:   sa.OrgID,
		Subject: sa.Subject,
		Name:    sa.Name,
		Status:  openapi.ServiceAccountStatusType(sa.Status),
		Groups:  sa.Groups,
	}, nil
}
