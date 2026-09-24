// Package apiscim serves SCIM 2.0 (RFC 7643, RFC 7644) for the control plane,
// so an identity provider can push the users and groups that approve sidecar
// reviews (ADR-0019). The protocol, filters and PATCH parsing come from
// github.com/elimity-com/scim; this package maps its resources onto hoop users
// and groups through gateway/services provisioning.
package apiscim

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/elimity-com/scim"
	scimerrors "github.com/elimity-com/scim/errors"
	"github.com/elimity-com/scim/optional"
	"github.com/elimity-com/scim/schema"
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/services"
	"gorm.io/gorm"
)

// maxResults caps a page. Identity providers look users and groups up one at
// a time by filter, so a page this size is only reached by a full listing.
const maxResults = 100

type orgKey struct{}

func orgFromRequest(r *http.Request) string {
	orgID, _ := r.Context().Value(orgKey{}).(string)
	return orgID
}

var (
	serverOnce sync.Once
	server     scim.Server
	serverErr  error
)

func newServer() (scim.Server, error) {
	// Entra ID sends booleans such as "active" as the strings "True" and
	// "False". Accepting them is the library's documented compatibility
	// switch for that client, and it only relaxes parsing.
	schema.SetAllowStringValues(true)
	return scim.NewServer(
		&scim.ServerArgs{
			ServiceProviderConfig: &scim.ServiceProviderConfig{
				MaxResults:       maxResults,
				SupportFiltering: true,
				SupportPatch:     true,
				AuthenticationSchemes: []scim.AuthenticationScheme{{
					Type:        scim.AuthenticationTypeOauthBearerToken,
					Name:        "Bearer token",
					Description: "The token generated in the control plane under Settings > Provisioning.",
					Primary:     true,
				}},
			},
			ResourceTypes: []scim.ResourceType{
				{
					ID:          optional.NewString("User"),
					Name:        "User",
					Endpoint:    "/Users",
					Description: optional.NewString("User Account"),
					Schema:      schema.CoreUserSchema(),
					SchemaExtensions: []scim.SchemaExtension{
						{Schema: schema.ExtensionEnterpriseUser()},
					},
					Handler: userHandler{},
				},
				{
					ID:          optional.NewString("Group"),
					Name:        "Group",
					Endpoint:    "/Groups",
					Description: optional.NewString("Group"),
					Schema:      schema.CoreGroupSchema(),
					Handler:     groupHandler{},
				},
			},
		},
		scim.WithBaseURL(BaseURL()),
	)
}

// BaseURL is the SCIM base URL an identity provider is configured with.
func BaseURL() string {
	return appconfig.Get().FullApiURL() + "/api/scim/v2"
}

// Handler serves every SCIM request under /api/scim/v2. It runs behind
// SCIMAuthMiddleware, which names the organization.
//
// A gateway answers 412 after authentication, as /sidecars/reviews does: its
// groups are not provisioned, and no SCIM token can be created there.
func Handler(c *gin.Context) {
	orgID := apiroutes.SCIMOrgFromContext(c)
	if orgID == "" {
		abort(c, http.StatusUnauthorized, "access denied")
		return
	}
	if !appconfig.Get().IsControlPlane() {
		abort(c, http.StatusPreconditionFailed, "SCIM provisioning is served by the control plane")
		return
	}
	serverOnce.Do(func() { server, serverErr = newServer() })
	if serverErr != nil {
		log.Errorf("failed building the scim server, err=%v", serverErr)
		abort(c, http.StatusInternalServerError, "internal server error")
		return
	}

	req := c.Request.Clone(context.WithValue(c.Request.Context(), orgKey{}, orgID))
	// The library routes on paths that start with /v2.
	req.URL.Path = "/v2" + c.Param("path")
	req.URL.RawPath = ""
	server.ServeHTTP(c.Writer, req)
}

func abort(c *gin.Context, status int, detail string) {
	c.Header("Content-Type", "application/scim+json")
	c.AbortWithStatusJSON(status, scimerrors.ScimError{Status: status, Detail: detail})
}

// toSCIMError maps a provisioning error onto the SCIM error a client acts on:
// 404 lets it recreate, 409 lets it look the resource up and update it.
func toSCIMError(id string, err error) error {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return scimerrors.ScimErrorResourceNotFound(id)
	case errors.Is(err, services.ErrProvisionedUserExists),
		errors.Is(err, services.ErrProvisionedGroupExists):
		e := scimerrors.ScimErrorUniqueness
		e.Detail = err.Error()
		return e
	case errors.Is(err, services.ErrAmbiguousEmail):
		e := scimerrors.ScimErrorUniqueness
		e.Detail = fmt.Sprintf("%v; remove the duplicate user in hoop first", err)
		return e
	case errors.Is(err, services.ErrProvisionedUserEmailRequired):
		return scimerrors.ScimErrorBadRequest(err.Error())
	}
	log.Errorf("scim request failed, id=%s, err=%v", id, err)
	return scimerrors.ScimError{Status: http.StatusInternalServerError, Detail: "internal server error"}
}

// page applies a list request's filter and window to resources that were
// already built, which is how the library's filter validator works.
func page(resources []scim.Resource, params scim.ListRequestParams) (scim.Page, error) {
	var matched []scim.Resource
	for _, r := range resources {
		if params.FilterValidator != nil {
			attrs := make(map[string]any, len(r.Attributes)+2)
			for k, v := range r.Attributes {
				attrs[k] = v
			}
			attrs[schema.CommonAttributeID] = r.ID
			if r.ExternalID.Present() {
				attrs[schema.CommonAttributeExternalID] = r.ExternalID.Value()
			}
			if err := params.FilterValidator.PassesFilter(attrs); err != nil {
				continue
			}
		}
		matched = append(matched, r)
	}

	out := scim.Page{TotalResults: len(matched), Resources: []scim.Resource{}}
	start := params.StartIndex - 1
	if start < 0 {
		start = 0
	}
	if params.Count <= 0 || start >= len(matched) {
		return out, nil
	}
	end := start + params.Count
	if end > len(matched) {
		end = len(matched)
	}
	out.Resources = matched[start:end]
	return out, nil
}
