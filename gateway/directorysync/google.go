package directorysync

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/oauth2/google"
	admin "google.golang.org/api/admin/directory/v1"
	"google.golang.org/api/option"
)

// googleProvider reads Google Workspace. Groups are named by their email,
// which is what hoop's Google login stores as the group name (idp/oidc
// gsuite.go), so a synced user and a user who logs in carry the same names.
type googleProvider struct {
	svc      *admin.Service
	customer string
}

func newGoogleProvider(ctx context.Context, s GoogleSettings, opts ...option.ClientOption) (*googleProvider, error) {
	if len(opts) == 0 {
		cfg, err := google.JWTConfigFromJSON([]byte(s.ServiceAccountJSON),
			admin.AdminDirectoryGroupReadonlyScope,
			admin.AdminDirectoryGroupMemberReadonlyScope,
			admin.AdminDirectoryUserReadonlyScope,
		)
		if err != nil {
			return nil, fmt.Errorf("invalid google service account: %w", err)
		}
		// Domain-wide delegation: the service account reads the directory
		// as this admin.
		cfg.Subject = s.AdminEmail
		opts = []option.ClientOption{option.WithHTTPClient(cfg.Client(ctx))}
	}
	svc, err := admin.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed creating the google directory client: %w", err)
	}
	customer := s.Customer
	if customer == "" {
		customer = "my_customer"
	}
	return &googleProvider{svc: svc, customer: customer}, nil
}

func (p *googleProvider) ListGroups(ctx context.Context) ([]Group, error) {
	var out []Group
	err := p.svc.Groups.List().Customer(p.customer).MaxResults(200).Pages(ctx, func(page *admin.Groups) error {
		for _, g := range page.Groups {
			out = append(out, Group{ID: g.Id, Name: g.Email})
		}
		return nil
	})
	return out, err
}

// ListMembers returns the users of the group, nested groups flattened. A
// member whose status is not ACTIVE (suspended, archived) is inactive.
func (p *googleProvider) ListMembers(ctx context.Context, groupID string) ([]User, error) {
	var out []User
	err := p.svc.Members.List(groupID).IncludeDerivedMembership(true).MaxResults(200).
		Pages(ctx, func(page *admin.Members) error {
			for _, m := range page.Members {
				if m.Type != "" && m.Type != "USER" {
					continue
				}
				out = append(out, User{
					ExternalID: m.Id,
					Email:      m.Email,
					Name:       m.Email,
					Active:     m.Status == "" || strings.EqualFold(m.Status, "ACTIVE"),
				})
			}
			return nil
		})
	return out, err
}
