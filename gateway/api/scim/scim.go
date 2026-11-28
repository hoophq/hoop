package apiscim

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const (
	scimContentType = "application/scim+json"
	scimOrgKey      = "scim-org-id"
	scimTokenPrefix = "hscim_"

	schemaUser          = "urn:ietf:params:scim:schemas:core:2.0:User"
	schemaGroup         = "urn:ietf:params:scim:schemas:core:2.0:Group"
	schemaListResponse  = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	schemaPatchOp       = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	schemaError         = "urn:ietf:params:scim:api:messages:2.0:Error"
	schemaServiceConfig = "urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"
	schemaResourceType  = "urn:ietf:params:scim:schemas:core:2.0:ResourceType"
)

func scimBaseURL() string {
	return strings.TrimSuffix(appconfig.Get().FullApiURL(), "/") + "/api/scim/v2"
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return scimTokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// --- Admin token management ---

// GetTokenStatus
//
//	@Summary		Get SCIM Token Status
//	@Description	Whether SCIM 2.0 provisioning is enabled and has an active bearer token.
//	@Tags			SCIM
//	@Produce		json
//	@Success		200	{object}	openapi.SCIMTokenStatus
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/scim/token [get]
func GetTokenStatus(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	out := openapi.SCIMTokenStatus{
		Enabled:     featureflag.IsEnabled(ctx.OrgID, featureflag.FlagSCIMProvisioning),
		SCIMBaseURL: scimBaseURL(),
	}
	tok, err := models.GetSCIMTokenByOrg(models.DB, ctx.OrgID)
	switch {
	case err == nil:
		out.Configured = true
		out.CreatedAt = &tok.CreatedAt
		out.RotatedAt = &tok.RotatedAt
	case !errors.Is(err, models.ErrNotFound):
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading SCIM token status")
		return
	}
	c.JSON(http.StatusOK, out)
}

// CreateOrRotateToken
//
//	@Summary		Create or Rotate SCIM Token
//	@Description	Generates a new SCIM 2.0 bearer token for the organization, replacing any previous token. The plaintext token is returned once.
//	@Tags			SCIM
//	@Produce		json
//	@Success		201	{object}	openapi.SCIMTokenCreated
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/scim/token [post]
func CreateOrRotateToken(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	raw, err := generateToken()
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed generating SCIM token")
		return
	}
	tok, err := models.UpsertSCIMToken(models.DB, ctx.OrgID, hashToken(raw), ctx.UserEmail)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed storing SCIM token")
		return
	}
	c.JSON(http.StatusCreated, openapi.SCIMTokenCreated{
		Token:       raw,
		SCIMBaseURL: scimBaseURL(),
		CreatedAt:   tok.CreatedAt,
		RotatedAt:   tok.RotatedAt,
	})
}

// DeleteToken
//
//	@Summary		Delete SCIM Token
//	@Description	Revokes the organization's SCIM 2.0 bearer token.
//	@Tags			SCIM
//	@Success		204
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/scim/token [delete]
func DeleteToken(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if err := models.DeleteSCIMToken(models.DB, ctx.OrgID); err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed revoking SCIM token")
		return
	}
	c.Status(http.StatusNoContent)
}

// --- SCIM 2.0 routes & auth ---

// RegisterSCIMRoutes mounts the RFC 7644 SCIM 2.0 endpoints under /scim/v2.
func RegisterSCIMRoutes(r *apiroutes.Router) {
	RegisterSCIMGroup(r.RouterGroup)
}

// RegisterSCIMGroup mounts /scim/v2 on any Gin RouterGroup (also used by tests).
func RegisterSCIMGroup(rg *gin.RouterGroup) {
	v2 := rg.Group("/scim/v2", SCIMAuthMiddleware)
	v2.GET("/ServiceProviderConfig", GetServiceProviderConfig)
	v2.GET("/ResourceTypes", ListResourceTypes)
	v2.GET("/Schemas", ListSchemas)

	v2.GET("/Users", ListUsers)
	v2.GET("/Users/:id", GetUser)
	v2.POST("/Users", CreateUser)
	v2.PUT("/Users/:id", ReplaceUser)
	v2.PATCH("/Users/:id", PatchUser)
	v2.DELETE("/Users/:id", DeleteUser)

	v2.GET("/Groups", ListGroups)
	v2.GET("/Groups/:id", GetGroup)
	v2.POST("/Groups", CreateGroup)
	v2.PUT("/Groups/:id", ReplaceGroup)
	v2.PATCH("/Groups/:id", PatchGroup)
	v2.DELETE("/Groups/:id", DeleteGroup)
}

func writeSCIM(c *gin.Context, status int, body any) {
	c.Header("Content-Type", scimContentType)
	c.JSON(status, body)
}

func writeSCIMError(c *gin.Context, status int, detail string) {
	c.Header("Content-Type", scimContentType)
	c.AbortWithStatusJSON(status, map[string]any{
		"schemas": []string{schemaError},
		"status":  strconv.Itoa(status),
		"detail":  detail,
	})
}

// SCIMAuthMiddleware authenticates a SCIM 2.0 bearer token and requires
// experimental.scim_provisioning to be enabled for that organization.
func SCIMAuthMiddleware(c *gin.Context) {
	hdr := c.GetHeader("Authorization")
	raw, ok := strings.CutPrefix(hdr, "Bearer ")
	if !ok || strings.TrimSpace(raw) == "" {
		writeSCIMError(c, http.StatusUnauthorized, "missing bearer token")
		return
	}
	tok, err := models.GetSCIMTokenByHash(models.DB, hashToken(strings.TrimSpace(raw)))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			writeSCIMError(c, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		writeSCIMError(c, http.StatusInternalServerError, "failed validating SCIM token")
		return
	}
	if !featureflag.IsEnabled(tok.OrgID, featureflag.FlagSCIMProvisioning) {
		writeSCIMError(c, http.StatusForbidden, "SCIM provisioning is disabled for this organization")
		return
	}
	c.Set(scimOrgKey, tok.OrgID)
	c.Next()
}

func scimOrgID(c *gin.Context) string {
	v, _ := c.Get(scimOrgKey)
	s, _ := v.(string)
	return s
}

// --- Discovery ---

func GetServiceProviderConfig(c *gin.Context) {
	writeSCIM(c, http.StatusOK, map[string]any{
		"schemas":               []string{schemaServiceConfig},
		"patch":                 map[string]any{"supported": true},
		"bulk":                  map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
		"filter":                map[string]any{"supported": true, "maxResults": 200},
		"changePassword":        map[string]any{"supported": false},
		"sort":                  map[string]any{"supported": false},
		"etag":                  map[string]any{"supported": false},
		"authenticationSchemes": []map[string]any{{"type": "oauthbearertoken", "name": "OAuth Bearer Token", "description": "Authentication scheme using the OAuth Bearer Token Standard"}},
	})
}

func ListResourceTypes(c *gin.Context) {
	writeSCIM(c, http.StatusOK, map[string]any{
		"schemas":      []string{schemaListResponse},
		"totalResults": 2,
		"Resources": []map[string]any{
			{"schemas": []string{schemaResourceType}, "id": "User", "name": "User", "endpoint": "/Users", "schema": schemaUser},
			{"schemas": []string{schemaResourceType}, "id": "Group", "name": "Group", "endpoint": "/Groups", "schema": schemaGroup},
		},
	})
}

func ListSchemas(c *gin.Context) {
	writeSCIM(c, http.StatusOK, map[string]any{
		"schemas":      []string{schemaListResponse},
		"totalResults": 2,
		"Resources": []map[string]any{
			{"id": schemaUser, "name": "User"},
			{"id": schemaGroup, "name": "Group"},
		},
	})
}

// --- Filter parsing ---

var eqFilterRE = regexp.MustCompile(`(?i)^\s*([a-z0-9._]+)\s+eq\s+"([^"]*)"\s*$`)

func parseEqFilter(raw string) (field, val string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil
	}
	m := eqFilterRE.FindStringSubmatch(raw)
	if len(m) != 3 {
		return "", "", fmt.Errorf("unsupported filter %q", raw)
	}
	switch strings.ToLower(m[1]) {
	case "username":
		return "userName", m[2], nil
	case "emails.value", "emails":
		return "email", m[2], nil
	case "externalid":
		return "externalId", m[2], nil
	case "displayname":
		return "displayName", m[2], nil
	default:
		return "", "", fmt.Errorf("unsupported filter attribute %q", m[1])
	}
}

func parsePagination(c *gin.Context) (startIndex, count int) {
	startIndex = 1
	count = 100
	if v, err := strconv.Atoi(c.Query("startIndex")); err == nil && v >= 1 {
		startIndex = v
	}
	if v, err := strconv.Atoi(c.Query("count")); err == nil && v > 0 && v <= 200 {
		count = v
	}
	return startIndex, count
}

// --- SCIM payloads ---

type scimName struct {
	Formatted  string `json:"formatted,omitempty"`
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
}

type scimEmail struct {
	Value   string `json:"value"`
	Type    string `json:"type,omitempty"`
	Primary bool   `json:"primary,omitempty"`
}

type scimUserPayload struct {
	Schemas     []string    `json:"schemas,omitempty"`
	ID          string      `json:"id,omitempty"`
	ExternalID  string      `json:"externalId,omitempty"`
	UserName    string      `json:"userName"`
	DisplayName string      `json:"displayName,omitempty"`
	Name        *scimName   `json:"name,omitempty"`
	Emails      []scimEmail `json:"emails,omitempty"`
	Active      *bool       `json:"active,omitempty"`
	Meta        *scimMeta   `json:"meta,omitempty"`
}

type scimMeta struct {
	ResourceType string `json:"resourceType"`
	Location     string `json:"location,omitempty"`
}

func toSCIMUser(u models.SCIMUser) scimUserPayload {
	active := u.Active()
	out := scimUserPayload{
		Schemas:     []string{schemaUser},
		ID:          u.ID,
		ExternalID:  u.ExternalID.String,
		UserName:    u.Email,
		DisplayName: u.Name,
		Name:        &scimName{Formatted: u.Name},
		Emails:      []scimEmail{{Value: u.Email, Type: "work", Primary: true}},
		Active:      &active,
		Meta:        &scimMeta{ResourceType: "User", Location: scimBaseURL() + "/Users/" + u.ID},
	}
	return out
}

func extractUserEmail(p scimUserPayload) string {
	for _, e := range p.Emails {
		if e.Primary && strings.TrimSpace(e.Value) != "" {
			return strings.ToLower(strings.TrimSpace(e.Value))
		}
	}
	for _, e := range p.Emails {
		if strings.TrimSpace(e.Value) != "" {
			return strings.ToLower(strings.TrimSpace(e.Value))
		}
	}
	return strings.ToLower(strings.TrimSpace(p.UserName))
}

func extractUserName(p scimUserPayload) string {
	if s := strings.TrimSpace(p.DisplayName); s != "" {
		return s
	}
	if p.Name != nil {
		if s := strings.TrimSpace(p.Name.Formatted); s != "" {
			return s
		}
		joined := strings.TrimSpace(strings.TrimSpace(p.Name.GivenName) + " " + strings.TrimSpace(p.Name.FamilyName))
		if joined != "" {
			return joined
		}
	}
	return ""
}

func validBareEmail(email string) bool {
	if email == "" || len(email) > 255 {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email
}

// --- Users handlers ---

func ListUsers(c *gin.Context) {
	field, val, err := parseEqFilter(c.Query("filter"))
	if err != nil {
		writeSCIMError(c, http.StatusBadRequest, err.Error())
		return
	}
	startIndex, count := parsePagination(c)
	users, total, err := models.ListSCIMUsers(models.DB, scimOrgID(c), field, val, startIndex, count)
	if err != nil {
		writeSCIMError(c, http.StatusInternalServerError, "failed listing users")
		return
	}
	resources := make([]scimUserPayload, 0, len(users))
	for _, u := range users {
		resources = append(resources, toSCIMUser(u))
	}
	writeSCIM(c, http.StatusOK, map[string]any{
		"schemas":      []string{schemaListResponse},
		"totalResults": total,
		"startIndex":   startIndex,
		"itemsPerPage": len(resources),
		"Resources":    resources,
	})
}

func GetUser(c *gin.Context) {
	u, err := models.GetSCIMUserByID(models.DB, scimOrgID(c), c.Param("id"))
	if errors.Is(err, models.ErrNotFound) {
		writeSCIMError(c, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeSCIMError(c, http.StatusInternalServerError, "failed loading user")
		return
	}
	writeSCIM(c, http.StatusOK, toSCIMUser(*u))
}

func CreateUser(c *gin.Context) {
	var req scimUserPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		writeSCIMError(c, http.StatusBadRequest, err.Error())
		return
	}
	email := extractUserEmail(req)
	if !validBareEmail(email) {
		writeSCIMError(c, http.StatusBadRequest, "userName or emails must contain a valid email address")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	u, _, err := models.CreateOrAdoptSCIMUser(models.DB, scimOrgID(c), email, extractUserName(req), strings.TrimSpace(req.ExternalID), active)
	if errors.Is(err, models.ErrAlreadyExists) {
		writeSCIMError(c, http.StatusConflict, "a user with this externalId already exists")
		return
	}
	if err != nil {
		writeSCIMError(c, http.StatusInternalServerError, "failed creating user")
		return
	}
	writeSCIM(c, http.StatusCreated, toSCIMUser(*u))
}

func ReplaceUser(c *gin.Context) {
	var req scimUserPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		writeSCIMError(c, http.StatusBadRequest, err.Error())
		return
	}
	email := extractUserEmail(req)
	if email != "" && !validBareEmail(email) {
		writeSCIMError(c, http.StatusBadRequest, "userName or emails must contain a valid email address")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	ext := strings.TrimSpace(req.ExternalID)
	u, err := models.UpdateSCIMUser(models.DB, scimOrgID(c), c.Param("id"), email, extractUserName(req), &ext, active)
	if errors.Is(err, models.ErrNotFound) {
		writeSCIMError(c, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeSCIMError(c, http.StatusInternalServerError, "failed updating user")
		return
	}
	writeSCIM(c, http.StatusOK, toSCIMUser(*u))
}

type scimPatchRequest struct {
	Schemas    []string            `json:"schemas"`
	Operations []scimPatchOpDetail `json:"Operations"`
}

type scimPatchOpDetail struct {
	Op    string          `json:"op"`
	Path  string          `json:"path,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

func PatchUser(c *gin.Context) {
	cur, err := models.GetSCIMUserByID(models.DB, scimOrgID(c), c.Param("id"))
	if errors.Is(err, models.ErrNotFound) {
		writeSCIMError(c, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeSCIMError(c, http.StatusInternalServerError, "failed loading user")
		return
	}
	var req scimPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeSCIMError(c, http.StatusBadRequest, err.Error())
		return
	}
	email := cur.Email
	name := cur.Name
	active := cur.Active()
	var ext *string
	if cur.ExternalID.Valid {
		s := cur.ExternalID.String
		ext = &s
	}

	applyField := func(path string, val any) {
		switch strings.ToLower(path) {
		case "active":
			switch v := val.(type) {
			case bool:
				active = v
			case string:
				active = strings.EqualFold(v, "true")
			}
		case "username":
			if s, ok := val.(string); ok && validBareEmail(strings.ToLower(strings.TrimSpace(s))) {
				email = strings.ToLower(strings.TrimSpace(s))
			}
		case "displayname", "name.formatted":
			if s, ok := val.(string); ok {
				name = strings.TrimSpace(s)
			}
		case "externalid":
			if s, ok := val.(string); ok {
				s = strings.TrimSpace(s)
				ext = &s
			}
		case "emails":
			if list, ok := val.([]any); ok {
				for _, item := range list {
					if m, ok := item.(map[string]any); ok {
						if s, ok := m["value"].(string); ok && validBareEmail(strings.ToLower(strings.TrimSpace(s))) {
							email = strings.ToLower(strings.TrimSpace(s))
							break
						}
					}
				}
			}
		}
	}

	for _, op := range req.Operations {
		verb := strings.ToLower(strings.TrimSpace(op.Op))
		if verb != "replace" && verb != "add" && verb != "remove" {
			writeSCIMError(c, http.StatusBadRequest, fmt.Sprintf("unsupported patch op %q", op.Op))
			return
		}
		if len(op.Value) == 0 {
			continue
		}
		if strings.TrimSpace(op.Path) == "" {
			var obj map[string]any
			if err := json.Unmarshal(op.Value, &obj); err == nil {
				for k, v := range obj {
					applyField(k, v)
				}
			}
			continue
		}
		var v any
		if err := json.Unmarshal(op.Value, &v); err == nil {
			applyField(op.Path, v)
		}
	}

	updated, err := models.UpdateSCIMUser(models.DB, scimOrgID(c), cur.ID, email, name, ext, active)
	if err != nil {
		writeSCIMError(c, http.StatusInternalServerError, "failed updating user")
		return
	}
	writeSCIM(c, http.StatusOK, toSCIMUser(*updated))
}

func DeleteUser(c *gin.Context) {
	err := models.DeactivateSCIMUser(models.DB, scimOrgID(c), c.Param("id"))
	if errors.Is(err, models.ErrNotFound) {
		writeSCIMError(c, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeSCIMError(c, http.StatusInternalServerError, "failed deactivating user")
		return
	}
	c.Status(http.StatusNoContent)
}

// --- Groups handlers ---

type scimGroupMemberPayload struct {
	Value   string `json:"value"`
	Display string `json:"display,omitempty"`
}

type scimGroupPayload struct {
	Schemas     []string                 `json:"schemas,omitempty"`
	ID          string                   `json:"id,omitempty"`
	ExternalID  string                   `json:"externalId,omitempty"`
	DisplayName string                   `json:"displayName"`
	Members     []scimGroupMemberPayload `json:"members"`
	Meta        *scimMeta                `json:"meta,omitempty"`
}

func toSCIMGroup(g models.SCIMGroup) scimGroupPayload {
	members := make([]scimGroupMemberPayload, 0, len(g.Members))
	for _, m := range g.Members {
		display := m.Name
		if display == "" {
			display = m.Email
		}
		members = append(members, scimGroupMemberPayload{Value: m.UserID, Display: display})
	}
	return scimGroupPayload{
		Schemas:     []string{schemaGroup},
		ID:          g.ID,
		ExternalID:  g.ExternalID.String,
		DisplayName: g.DisplayName,
		Members:     members,
		Meta:        &scimMeta{ResourceType: "Group", Location: scimBaseURL() + "/Groups/" + g.ID},
	}
}

func memberValues(members []scimGroupMemberPayload) []string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		if v := strings.TrimSpace(m.Value); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func ListGroups(c *gin.Context) {
	field, val, err := parseEqFilter(c.Query("filter"))
	if err != nil {
		writeSCIMError(c, http.StatusBadRequest, err.Error())
		return
	}
	startIndex, count := parsePagination(c)
	groups, total, err := models.ListSCIMGroups(models.DB, scimOrgID(c), field, val, startIndex, count)
	if err != nil {
		writeSCIMError(c, http.StatusInternalServerError, "failed listing groups")
		return
	}
	resources := make([]scimGroupPayload, 0, len(groups))
	for _, g := range groups {
		resources = append(resources, toSCIMGroup(g))
	}
	writeSCIM(c, http.StatusOK, map[string]any{
		"schemas":      []string{schemaListResponse},
		"totalResults": total,
		"startIndex":   startIndex,
		"itemsPerPage": len(resources),
		"Resources":    resources,
	})
}

func GetGroup(c *gin.Context) {
	g, err := models.GetSCIMGroupByID(models.DB, scimOrgID(c), c.Param("id"))
	if errors.Is(err, models.ErrNotFound) {
		writeSCIMError(c, http.StatusNotFound, "group not found")
		return
	}
	if err != nil {
		writeSCIMError(c, http.StatusInternalServerError, "failed loading group")
		return
	}
	writeSCIM(c, http.StatusOK, toSCIMGroup(*g))
}

func CreateGroup(c *gin.Context) {
	var req scimGroupPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		writeSCIMError(c, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.DisplayName)
	if name == "" {
		writeSCIMError(c, http.StatusBadRequest, "displayName is required")
		return
	}
	g, err := models.CreateSCIMGroup(models.DB, scimOrgID(c), name, strings.TrimSpace(req.ExternalID), memberValues(req.Members))
	if errors.Is(err, models.ErrAlreadyExists) {
		writeSCIMError(c, http.StatusConflict, "a group with this displayName or externalId already exists")
		return
	}
	if err != nil {
		writeSCIMError(c, http.StatusInternalServerError, "failed creating group")
		return
	}
	writeSCIM(c, http.StatusCreated, toSCIMGroup(*g))
}

func ReplaceGroup(c *gin.Context) {
	var req scimGroupPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		writeSCIMError(c, http.StatusBadRequest, err.Error())
		return
	}
	ext := strings.TrimSpace(req.ExternalID)
	g, err := models.ReplaceSCIMGroup(models.DB, scimOrgID(c), c.Param("id"), req.DisplayName, &ext, memberValues(req.Members))
	if errors.Is(err, models.ErrNotFound) {
		writeSCIMError(c, http.StatusNotFound, "group not found")
		return
	}
	if err != nil {
		writeSCIMError(c, http.StatusInternalServerError, "failed updating group")
		return
	}
	writeSCIM(c, http.StatusOK, toSCIMGroup(*g))
}

var memberPathFilterRE = regexp.MustCompile(`(?i)^members\[\s*value\s+eq\s+"([^"]+)"\s*\]$`)

func extractMemberIDs(raw json.RawMessage) []string {
	var list []scimGroupMemberPayload
	if err := json.Unmarshal(raw, &list); err == nil {
		return memberValues(list)
	}
	var single scimGroupMemberPayload
	if err := json.Unmarshal(raw, &single); err == nil && strings.TrimSpace(single.Value) != "" {
		return []string{strings.TrimSpace(single.Value)}
	}
	return nil
}

func PatchGroup(c *gin.Context) {
	var req scimPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeSCIMError(c, http.StatusBadRequest, err.Error())
		return
	}
	var newDisplayName *string
	var replaceMembers, addMembers, removeMembers []string
	var hasReplaceMembers bool

	for _, op := range req.Operations {
		verb := strings.ToLower(strings.TrimSpace(op.Op))
		path := strings.TrimSpace(op.Path)
		lowerPath := strings.ToLower(path)

		if m := memberPathFilterRE.FindStringSubmatch(path); len(m) == 2 && verb == "remove" {
			removeMembers = append(removeMembers, m[1])
			continue
		}
		if lowerPath == "displayname" && (verb == "replace" || verb == "add") {
			var s string
			if err := json.Unmarshal(op.Value, &s); err == nil {
				newDisplayName = &s
			}
			continue
		}
		if lowerPath == "members" {
			ids := extractMemberIDs(op.Value)
			switch verb {
			case "add":
				addMembers = append(addMembers, ids...)
			case "remove":
				removeMembers = append(removeMembers, ids...)
			case "replace":
				hasReplaceMembers = true
				replaceMembers = ids
			}
			continue
		}
		if path == "" && (verb == "replace" || verb == "add") {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(op.Value, &obj); err == nil {
				if rawName, ok := obj["displayName"]; ok {
					var s string
					if err := json.Unmarshal(rawName, &s); err == nil {
						newDisplayName = &s
					}
				}
				if rawMembers, ok := obj["members"]; ok {
					ids := extractMemberIDs(rawMembers)
					if verb == "add" {
						addMembers = append(addMembers, ids...)
					} else {
						hasReplaceMembers = true
						replaceMembers = ids
					}
				}
			}
		}
	}

	g, err := models.PatchSCIMGroup(models.DB, scimOrgID(c), c.Param("id"), newDisplayName, replaceMembers, addMembers, removeMembers, hasReplaceMembers)
	if errors.Is(err, models.ErrNotFound) {
		writeSCIMError(c, http.StatusNotFound, "group not found")
		return
	}
	if err != nil {
		writeSCIMError(c, http.StatusInternalServerError, "failed patching group")
		return
	}
	writeSCIM(c, http.StatusOK, toSCIMGroup(*g))
}

func DeleteGroup(c *gin.Context) {
	err := models.DeleteSCIMGroup(models.DB, scimOrgID(c), c.Param("id"))
	if errors.Is(err, models.ErrNotFound) {
		writeSCIMError(c, http.StatusNotFound, "group not found")
		return
	}
	if err != nil {
		writeSCIMError(c, http.StatusInternalServerError, "failed deleting group")
		return
	}
	c.Status(http.StatusNoContent)
}
