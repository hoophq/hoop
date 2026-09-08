package apiconnections

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/transport/connectionrequests"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
)

type Review struct {
	ApprovalGroups []string `json:"groups"`
}

// Post Connection
//
//	@Summary				Create Connection
//	@description.markdown	api-connection
//	@Tags					Connections
//	@Accept					json
//	@Produce				json
//	@Param					request			body		openapi.Connection	true	"The request body resource"
//	@Success				201				{object}	openapi.Connection
//	@Failure				400,409,422,500	{object}	openapi.HTTPError
//	@Router					/connections [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.Connection
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if err := validateConnectionRequest(req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	sidecarID, err := resolveSidecarAssignment(ctx.OrgID, req.SidecarID, req.Type, req.SubType)
	if err != nil {
		abortAssignment(c, err)
		return
	}
	opaConfigID, err := resolveOPAConfigAssignment(ctx.OrgID, req.OPAConfigID)
	if err != nil {
		abortAssignment(c, err)
		return
	}
	existingConn, err := models.GetConnectionByNameOrID(ctx, req.Name)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching existing connection: %v", err)
		return
	}
	if existingConn != nil {
		c.JSON(http.StatusConflict, gin.H{"message": "Connection already exists."})
		return
	}

	setConnectionDefaults(&req)

	req.ID = uuid.NewString()
	req.Status = models.ConnectionStatusOffline
	if streamclient.IsAgentOnline(streamtypes.NewStreamID(req.AgentId, "")) {
		req.Status = models.ConnectionStatusOnline
	}

	envs := CoerceToMapString(req.Secrets)
	var secretsUpdatedAt *time.Time
	if len(envs) > 0 {
		now := time.Now().UTC()
		secretsUpdatedAt = &now
	}

	resp, err := models.UpsertConnection(ctx, &models.Connection{
		ID:                      req.ID,
		OrgID:                   ctx.OrgID,
		ResourceName:            req.ResourceName,
		AgentID:                 sql.NullString{String: req.AgentId, Valid: true},
		SidecarID:               sidecarID,
		OPAConfigID:             opaConfigID,
		Name:                    req.Name,
		Command:                 req.Command,
		Type:                    req.Type,
		SubType:                 sql.NullString{String: req.SubType, Valid: true},
		Envs:                    envs,
		Status:                  req.Status,
		ManagedBy:               sql.NullString{},
		Tags:                    req.Tags,
		AccessModeRunbooks:      req.AccessModeRunbooks,
		AccessModeExec:          req.AccessModeExec,
		AccessModeConnect:       req.AccessModeConnect,
		AccessSchema:            req.AccessSchema,
		Reviewers:               req.Reviewers,
		RedactTypes:             req.RedactTypes,
		GuardRailRules:          req.GuardRailRules,
		JiraIssueTemplateID:     sql.NullString{String: req.JiraIssueTemplateID, Valid: true},
		ConnectionTags:          req.ConnectionTags,
		ForceApproveGroups:      req.ForceApproveGroups,
		AccessMaxDuration:       req.AccessMaxDuration,
		MinReviewApprovals:      req.MinReviewApprovals,
		MandatoryMetadataFields: req.MandatoryMetadataFields,
		SecretsUpdatedAt:        secretsUpdatedAt,
	})

	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating connection: %v", err)
		return
	}

	if err := upsertConnectionAttributes(ctx, resp.Name, req.Attributes); err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed upserting connection attributes: %v", err)
		return
	}
	resp.Attributes = req.Attributes

	// Reconcile machine identity credentials based on attribute overlap
	if err := services.ReconcileAllMachineIdentitiesForConnection(context.Background(), ctx.OrgID, resp.Name); err != nil {
		log.Warnf("failed reconciling MI credentials after creating connection %s: %v", resp.Name, err)
	}

	// resp carries the envs as saved, which is what the adoption must match
	// the login against — not req.Secrets, which the caller controls and which
	// PUT may have only partially overlaid.
	out := ToOpenApi(resp, ctx.OrgHideRoleInfo)
	out.MCPOAuthWarning = AdoptMCPOAuthGrant(ctx.OrgID, resp, req.SubType, req.MCPOAuthFlowID)

	c.JSON(http.StatusCreated, out)
}

// Put Connection
//
//	@Summary		Update Connection
//	@Description	Update a connection resource
//	@Tags			Connections
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID		path		string				true	"The name or ID of the resource"
//	@Param			request			body		openapi.Connection	true	"The request body resource"
//	@Success		200				{object}	openapi.Connection
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID} [put]
func Put(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connNameOrID := c.Param("nameOrID")
	conn, err := models.GetConnectionByNameOrID(ctx, connNameOrID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching connection: %v", err)
		return
	}
	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}
	// when the connection is managed by the agent, make sure to deny any change
	if conn.ManagedBy.String != "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "unable to update a connection managed by its agent"})
		return
	}

	var req openapi.Connection
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if err := validateConnectionRequest(req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	sidecarID := conn.SidecarID
	if req.SidecarID != nil {
		resolved, err := resolveSidecarAssignment(ctx.OrgID, req.SidecarID, req.Type, req.SubType)
		if err != nil {
			abortAssignment(c, err)
			return
		}
		sidecarID = resolved
	} else {
		sidecarID = sql.NullString{String: "", Valid: false}
	}

	opaConfigID := conn.OPAConfigID
	if req.OPAConfigID != nil {
		resolved, err := resolveOPAConfigAssignment(ctx.OrgID, req.OPAConfigID)
		if err != nil {
			abortAssignment(c, err)
			return
		}
		opaConfigID = resolved
	} else {
		opaConfigID = sql.NullString{String: "", Valid: false}
	}
	setConnectionDefaults(&req)

	// immutable fields
	req.ID = conn.ID
	req.Name = conn.Name
	req.Status = models.ConnectionStatusOffline
	if streamclient.IsAgentOnline(streamtypes.NewStreamID(req.AgentId, "")) {
		req.Status = models.ConnectionStatusOnline
	}

	// PUT keeps replace-the-whole-map semantics for legacy clients. Track the
	// timestamp only when the resulting envs actually differ from what we had.
	var newEnvs map[string]string
	// When the org blocks reading role secrets, the client receives masked
	// envvars and cannot round-trip them. Overlay the incoming values onto
	// the stored ones so only the fields that are not empty are updated.
	if ctx.OrgHideRoleInfo {
		updatedEnvs := CoerceToMapNullableString(req.Secrets)
		newEnvs = overlaySecrets(conn.Envs, updatedEnvs)
	} else {
		newEnvs = CoerceToMapString(req.Secrets)
	}
	secretsUpdatedAt := conn.SecretsUpdatedAt
	if !envsEqual(conn.Envs, newEnvs) {
		now := time.Now().UTC()
		secretsUpdatedAt = &now
	}

	resp, err := models.UpsertConnection(ctx, &models.Connection{
		ID:                      conn.ID,
		OrgID:                   conn.OrgID,
		ResourceName:            req.ResourceName,
		AgentID:                 sql.NullString{String: req.AgentId, Valid: true},
		SidecarID:               sidecarID,
		OPAConfigID:             opaConfigID,
		Name:                    conn.Name,
		Command:                 req.Command,
		Type:                    req.Type,
		SubType:                 sql.NullString{String: req.SubType, Valid: true},
		Envs:                    newEnvs,
		Status:                  req.Status,
		ManagedBy:               sql.NullString{},
		Tags:                    req.Tags,
		AccessModeRunbooks:      req.AccessModeRunbooks,
		AccessModeExec:          req.AccessModeExec,
		AccessModeConnect:       req.AccessModeConnect,
		AccessSchema:            req.AccessSchema,
		Reviewers:               req.Reviewers,
		RedactTypes:             req.RedactTypes,
		GuardRailRules:          req.GuardRailRules,
		JiraIssueTemplateID:     sql.NullString{String: req.JiraIssueTemplateID, Valid: true},
		ConnectionTags:          req.ConnectionTags,
		ForceApproveGroups:      req.ForceApproveGroups,
		AccessMaxDuration:       req.AccessMaxDuration,
		MinReviewApprovals:      req.MinReviewApprovals,
		MandatoryMetadataFields: req.MandatoryMetadataFields,
		SecretsUpdatedAt:        secretsUpdatedAt,
	})

	if err != nil {
		switch err.(type) {
		case *models.ErrNotFoundGuardRailRules:
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		default:
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating connection: %v", err)
		}
		return
	}

	if err := upsertConnectionAttributes(ctx, resp.Name, req.Attributes); err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed upserting connection attributes: %v", err)
		return
	}
	resp.Attributes = req.Attributes

	// Reconcile machine identity credentials based on attribute overlap
	if err := services.ReconcileAllMachineIdentitiesForConnection(context.Background(), ctx.OrgID, resp.Name); err != nil {
		log.Warnf("failed reconciling MI credentials after updating connection %s: %v", resp.Name, err)
	}

	out := ToOpenApi(resp, ctx.OrgHideRoleInfo)
	out.MCPOAuthWarning = AdoptMCPOAuthGrant(ctx.OrgID, resp, req.SubType, req.MCPOAuthFlowID)

	c.JSON(http.StatusOK, out)
}

// Patch Connection
//
//	@Summary		Patch Connection
//	@Description	Partial update of a connection resource. Only provided fields will be updated.
//	@Tags			Connections
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID		path		string					true	"The name or ID of the resource"
//	@Param			request			body		openapi.ConnectionPatch	true	"The request body resource with fields to update"
//	@Success		200				{object}	openapi.Connection
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID} [patch]
func Patch(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connNameOrID := c.Param("nameOrID")
	conn, err := models.GetConnectionByNameOrID(ctx, connNameOrID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching connection: %v", err)
		return
	}
	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}
	// when the connection is managed by the agent, make sure to deny any change
	if conn.ManagedBy.String != "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "unable to update a connection managed by its agent"})
		return
	}

	var req openapi.ConnectionPatch
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	// Validate the body
	if err := validatePatchConnectionRequest(req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	// Apply patches from request (only non-nil values override)
	if req.Command != nil {
		conn.Command = *req.Command
	}
	if req.Type != nil {
		conn.Type = *req.Type
	}
	if req.SubType != nil {
		conn.SubType = sql.NullString{String: *req.SubType, Valid: *req.SubType != ""}
	}
	if req.Secrets != nil {
		var secrets map[string]string
		if ctx.OrgHideRoleInfo {
			updatedEnvs := CoerceToMapNullableString(*req.Secrets)
			secrets = overlaySecrets(conn.Envs, updatedEnvs)
		} else {
			secrets = CoerceToMapString(*req.Secrets)
		}

		conn.Envs = secrets

		now := time.Now().UTC()
		conn.SecretsUpdatedAt = &now
	}
	if req.AgentId != nil {
		conn.AgentID = sql.NullString{String: *req.AgentId, Valid: *req.AgentId != ""}
	}
	// Checked against the patched type, not the stored one. An untouched
	// assignment is checked too: a bare subtype change must not strand a
	// sidecar with a lane it cannot serve.
	if req.SidecarID != nil {
		resolved, err := resolveSidecarAssignment(ctx.OrgID, req.SidecarID, conn.Type, conn.SubType.String)
		if err != nil {
			abortAssignment(c, err)
			return
		}
		conn.SidecarID = resolved
	} else if err := revalidateSidecarAssignment(conn.SidecarID, conn.Type, conn.SubType.String); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	if req.OPAConfigID != nil {
		resolved, err := resolveOPAConfigAssignment(ctx.OrgID, req.OPAConfigID)
		if err != nil {
			abortAssignment(c, err)
			return
		}
		conn.OPAConfigID = resolved
	}
	if req.Reviewers != nil {
		conn.Reviewers = *req.Reviewers
	}
	if req.RedactTypes != nil {
		conn.RedactTypes = *req.RedactTypes
	}
	if req.Tags != nil {
		conn.Tags = *req.Tags
	}
	if req.ConnectionTags != nil {
		conn.ConnectionTags = *req.ConnectionTags
	}
	if req.AccessModeRunbooks != nil {
		conn.AccessModeRunbooks = *req.AccessModeRunbooks
	}
	if req.AccessModeExec != nil {
		conn.AccessModeExec = *req.AccessModeExec
	}
	if req.AccessModeConnect != nil {
		conn.AccessModeConnect = *req.AccessModeConnect
	}
	if req.AccessSchema != nil {
		conn.AccessSchema = *req.AccessSchema
	}
	if req.GuardRailRules != nil {
		conn.GuardRailRules = *req.GuardRailRules
	}
	if req.JiraIssueTemplateID != nil {
		conn.JiraIssueTemplateID = sql.NullString{String: *req.JiraIssueTemplateID, Valid: *req.JiraIssueTemplateID != ""}
	}

	if req.MandatoryMetadataFields != nil {
		conn.MandatoryMetadataFields = *req.MandatoryMetadataFields
	}

	// Update status
	conn.Status = models.ConnectionStatusOffline
	if streamclient.IsAgentOnline(streamtypes.NewStreamID(conn.AgentID.String, "")) {
		conn.Status = models.ConnectionStatusOnline
	}

	resp, err := models.UpsertConnection(ctx, conn)
	if err != nil {
		switch err.(type) {
		case *models.ErrNotFoundGuardRailRules:
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		default:
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed patching connection: %v", err)
		}
		return
	}

	if req.Attributes != nil {
		if err := upsertConnectionAttributes(ctx, resp.Name, *req.Attributes); err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed upserting connection attributes: %v", err)
			return
		}
		resp.Attributes = *req.Attributes

		// Reconcile machine identity credentials based on attribute overlap
		if err := services.ReconcileAllMachineIdentitiesForConnection(context.Background(), ctx.OrgID, resp.Name); err != nil {
			log.Warnf("failed reconciling MI credentials after patching connection %s: %v", resp.Name, err)
		}
	}

	out := ToOpenApi(resp, ctx.OrgHideRoleInfo)
	// Same adoption POST and PUT perform. The subtype comes off the stored
	// connection rather than the request: PATCH is partial, so a save that
	// only re-authorizes sends no subtype at all.
	if req.MCPOAuthFlowID != nil {
		out.MCPOAuthWarning = AdoptMCPOAuthGrant(ctx.OrgID, resp, resp.SubType.String, *req.MCPOAuthFlowID)
	}
	c.JSON(http.StatusOK, out)
}

// DeleteConnection
//
//	@Summary		Delete Connection
//	@Description	Delete a connection resource.
//	@Tags			Connections
//	@Produce		json
//	@Param			nameOrID	path	string	true	"Name or UUID of the connection"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connName := c.Param("nameOrID")
	if connName == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "missing connection name"})
		return
	}

	err := models.DeleteConnection(ctx.OrgID, connName)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
	case nil:
		connectionrequests.InvalidateSyncCache(ctx.OrgID, connName)
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed removing connection")
	}
}

// List Connections
//
//	@Summary		List Connections
//	@Description	List all connections.
//	@Tags			Connections
//	@Produce		json
//	@Param			agent_id		query		string											false	"Filter by agent id"																	Format(uuid)
//	@Param			tags			query		string											false	"DEPRECATED: Filter by tags, separated by comma"										Format(string)
//	@Param			tag_selector	query		string											false	"Selector tags to fo filter on, supports '=' and '!=' (e.g. key1=value1,key2=value2)"	Format(string)
//	@Param			search			query		string											false	"Search by name, type, subtype, resource name or status"								Format(string)
//	@Param			type			query		string											false	"Filter by type"																		Format(string)
//	@Param			subtype			query		string											false	"Filter by subtype"																		Format(string)
//	@Param			managed_by		query		string											false	"Filter by managed by"																	Format(string)
//	@Param			resource_name	query		string											false	"Filter by resource name"																Format(string)
//	@Param			attribute		query		string											false	"Filter by attributes, separated by comma"												Format(string)
//	@Param			connection_ids	query		string											false	"Filter by specific connection IDs, separated by comma"									Format(string)
//	@Param			page_size		query		int												false	"Maximum number of items to return (1-100). When provided, enables pagination"			Format(int)
//	@Param			page			query		int												false	"Page number (1-based). When provided, enables pagination"								Format(int)
//	@Success		200				{object}	openapi.PaginatedResponse[openapi.Connection]	"Returns Connection objects paginated when using pagination"
//	@Success		200				{array}		openapi.Connection								"Returns array of Connection objects"
//	@Failure		422,500			{object}	openapi.HTTPError
//	@Router			/connections [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	filterOpts, err := validateListOptions(c.Request.URL.Query())
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	urlValues := c.Request.URL.Query()
	pageStr := urlValues.Get("page")
	pageSizeStr := urlValues.Get("page_size")

	hasPaginationParams := pageStr != "" || pageSizeStr != ""

	if hasPaginationParams {
		page, pageSize, paginationErr := apivalidation.ParsePaginationParams(pageStr, pageSizeStr)

		// Use paginated response
		if paginationErr != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": paginationErr.Error()})
			return
		}

		// Set default page size if not provided but page is provided
		if pageSize == 0 && page > 0 {
			pageSize = 50 // Default page size
		}

		paginationOpts := models.ConnectionPaginationOption{
			ConnectionFilterOption: filterOpts,
			Page:                   page,
			PageSize:               pageSize,
		}

		connList, total, err := models.ListConnectionsPaginated(ctx.GetOrgID(), ctx.GetUserGroups(), paginationOpts)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing connections with pagination: %v", err)
			return
		}

		responseConnList := make([]openapi.Connection, len(connList))
		for i, conn := range connList {
			// it should return empty to avoid leaking sensitive content
			// in the future we plan to know which entry is sensitive or not
			conn.Envs = map[string]string{}
			responseConnList[i] = ToOpenApi(&conn, ctx.OrgHideRoleInfo)
		}

		response := openapi.PaginatedResponse[openapi.Connection]{
			Pages: openapi.Pagination{
				Total: int(total),
				Page:  page,
				Size:  pageSize,
			},
			Data: responseConnList,
		}

		c.JSON(http.StatusOK, response)
		return
	}

	// Use traditional non-paginated response
	connList, err := models.ListConnections(ctx, filterOpts)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing connections: %v", err)
		return
	}
	responseConnList := []openapi.Connection{}
	for _, conn := range connList {
		// it should return empty to avoid leaking sensitive content
		// in the future we plan to know which entry is sensitive or not
		conn.Envs = map[string]string{}
		responseConnList = append(responseConnList, ToOpenApi(&conn, ctx.OrgHideRoleInfo))

	}
	c.JSON(http.StatusOK, responseConnList)
}

// Get Connection
//
//	@Summary		Get Connection
//	@Description	Get resource by name or id
//	@Tags			Connections
//	@Param			nameOrID	path	string	true	"Name or UUID of the connection"
//	@Produce		json
//	@Success		200		{object}	openapi.Connection
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	conn, err := models.GetBareConnectionByNameOrID(ctx, c.Param("nameOrID"), models.DB)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching connection: %v", err)
		return
	}
	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	apiConn := ToOpenApi(conn, ctx.OrgHideRoleInfo)

	// Derived enrichment: a failure here must not take the whole connection down, but it
	// must also not be reported as "nothing is active" — that is indistinguishable from a
	// genuinely unprotected connection. Leaving effective_features null says "unknown".
	//
	// jit_access_duration_sec is set independently of that. It predates this object and
	// the native client reads it to decide whether to expect a review, so a failure in an
	// unrelated resolver must not take it down with them. It stays nil only when the JIT
	// lookup itself failed or found nothing.
	features, jitDuration, err := resolveEffectiveFeatures(ctx.OrgID, conn)
	apiConn.JitAccessDurationSec = jitDuration
	if err != nil {
		log.With("connection", conn.Name).Errorf("failed resolving effective features, err=%v", err)
	} else {
		apiConn.EffectiveFeatures = features
	}

	apiConn.MCPOAuthGranted = hasMCPOAuthGrant(ctx.OrgID, conn)

	c.JSON(http.StatusOK, apiConn)
}

// resolveEffectiveFeatures reports which features will actually act on this connection,
// and the JIT duration when a JIT rule applies.
//
// The stored columns are not sufficient. Guardrails, data masking and access requests can
// be attached either directly or through an attribute — the mechanism protection profiles
// use — and the runtime resolves the union of both. These are the same resolvers the
// enforcement path calls (gateway/transport/client.go and the accessrequest interceptor),
// composed the same way gateway/analytics/segment.go composes them, so what we report here
// cannot drift from what actually happens.
func resolveEffectiveFeatures(orgID string, conn *models.Connection) (*openapi.ConnectionEffectiveFeatures, *int, error) {
	parsedOrgID, err := uuid.Parse(orgID)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing org id: %w", err)
	}

	var (
		guardrails  *models.ConnectionGuardRailRules
		maskingRule json.RawMessage
		commandRule *models.AccessRequestRule
		jitRule     *models.AccessRequestRule
		hasAnalyzer bool
	)

	group, _ := errgroup.WithContext(context.Background())
	group.Go(func() error {
		rules, err := services.GetGuardRailRulesForConnection(orgID, conn.Name)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("guardrails: %w", err)
		}
		guardrails = rules
		return nil
	})
	group.Go(func() error {
		// Mirrors the runtime gate: masking only runs when the provider is active,
		// so reporting rules without it would promise something that never happens.
		if appconfig.Get().DlpProvider() != "mspresidio" {
			return nil
		}
		rules, err := services.GetDataMaskingRulesForConnection(orgID, conn.Name)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("data masking: %w", err)
		}
		maskingRule = rules
		return nil
	})
	group.Go(func() error {
		rule, err := services.GetRuleForConnection(parsedOrgID, conn.Name, "command")
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("command access request rule: %w", err)
		}
		commandRule = rule
		return nil
	})
	group.Go(func() error {
		rule, err := services.GetRuleForConnection(parsedOrgID, conn.Name, "jit")
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("jit access request rule: %w", err)
		}
		jitRule = rule
		return nil
	})
	group.Go(func() error {
		rule, err := models.GetAISessionAnalyzerRuleByConnection(models.DB, parsedOrgID, conn.Name)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("ai session analyzer rule: %w", err)
		}
		hasAnalyzer = rule != nil
		return nil
	})

	waitErr := group.Wait()

	// Computed before the error check on purpose: the group is not cancellable, so every
	// resolver runs to completion and jitRule is populated whenever its own lookup
	// succeeded. Returning it alongside the error keeps one resolver's failure from
	// suppressing another's result.
	var jitDuration *int
	if jitRule != nil {
		jitDuration = jitRule.AccessMaxDuration
	}

	if waitErr != nil {
		return nil, jitDuration, waitErr
	}

	return &openapi.ConnectionEffectiveFeatures{
		Guardrails:        guardrails != nil && !guardrails.HasEmptyRules(),
		DataMasking:       hasDataMaskingRules(maskingRule),
		AISessionAnalyzer: hasAnalyzer,
		JiraTemplates:     conn.JiraIssueTemplateID.String != "",
		MandatoryMetadata: len(conn.MandatoryMetadataFields) > 0,
		AccessRequest: openapi.ConnectionAccessRequestFeatures{
			Command:         commandRule != nil,
			Jit:             jitRule != nil,
			LegacyReviewers: len(conn.Reviewers) > 0,
		},
	}, jitDuration, nil
}

// hasDataMaskingRules reports whether the masking resolver returned any rule. It
// coalesces to the literal "[]" when nothing matches, so an emptiness check on the
// raw bytes alone would read that as active.
func hasDataMaskingRules(raw json.RawMessage) bool {
	var rules []any
	if err := json.Unmarshal(raw, &rules); err != nil {
		return false
	}
	return len(rules) > 0
}

func ToOpenApi(conn *models.Connection, hideRoleInfo bool) openapi.Connection {
	var managedBy *string
	if conn.ManagedBy.Valid {
		managedBy = &conn.ManagedBy.String
	}
	// default_database is derived from the secret env map (envvar:DB), so it
	// is subject to the same write-only policy as Secrets: returning it while
	// the map is masked would be a read-back path for an inline secret value.
	var defaultDB []byte
	if !hideRoleInfo {
		defaultDB, _ = base64.StdEncoding.DecodeString(conn.Envs["envvar:DB"])
		if len(defaultDB) == 0 {
			defaultDB = []byte(``)
		}
	}

	var publicEnvs map[string]any
	if hideRoleInfo {
		publicEnvs = stripInlineSecrets(conn.Envs)
	} else {
		publicEnvs = coerceToAnyMap(conn.Envs)
	}

	return openapi.Connection{
		ID:                      conn.ID,
		Name:                    conn.Name,
		ResourceName:            conn.ResourceName,
		Command:                 conn.Command,
		Type:                    conn.Type,
		SubType:                 conn.SubType.String,
		Secrets:                 publicEnvs,
		DefaultDatabase:         string(defaultDB),
		AgentId:                 conn.AgentID.String,
		SidecarID:               sidecarIDPtr(conn.SidecarID),
		OPAConfigID:             opaConfigIDPtr(conn.OPAConfigID),
		Status:                  conn.Status,
		Reviewers:               conn.Reviewers,
		RedactEnabled:           conn.RedactEnabled,
		RedactTypes:             conn.RedactTypes,
		ManagedBy:               managedBy,
		Tags:                    conn.Tags,
		ConnectionTags:          conn.ConnectionTags,
		AccessModeRunbooks:      conn.AccessModeRunbooks,
		AccessModeExec:          conn.AccessModeExec,
		AccessModeConnect:       conn.AccessModeConnect,
		AccessSchema:            conn.AccessSchema,
		GuardRailRules:          conn.GuardRailRules,
		JiraIssueTemplateID:     conn.JiraIssueTemplateID.String,
		ForceApproveGroups:      conn.ForceApproveGroups,
		AccessMaxDuration:       conn.AccessMaxDuration,
		MinReviewApprovals:      conn.MinReviewApprovals,
		MandatoryMetadataFields: conn.MandatoryMetadataFields,
		Attributes:              conn.Attributes,
		ManagedAttributes:       conn.ManagedAttributes,
		SecretsUpdatedAt:        conn.SecretsUpdatedAt,
	}
}

// Test Connection
//
//	@Summary		Test Connection
//	@Description	Test resource by name or id (only for database connections, it will attempt a simple ping).
//	@Tags			Connections
//	@Param			nameOrID	path	string	true	"Name or UUID of the connection"
//	@Produce		json
//	@Success		200		{object}	openapi.ConnectionTestResponse
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/test [get]
func TestConnection(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	conn, err := models.GetConnectionByNameOrID(ctx, c.Param("nameOrID"))
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching connection: %v", err)
		return
	}
	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
		return
	}

	testConnectionErr := testConnection(ctx, apiroutes.GetAccessTokenFromRequest(c), conn)
	if testConnectionErr != nil {
		log.Warnf("connection ping test failed, err=%v", testConnectionErr)
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("connection test failed: %v", testConnectionErr)})
		return
	}

	c.JSON(http.StatusOK, openapi.ConnectionTestResponse{
		Success: true,
	})
}

func getScriptsForTestConnection(connectionType *pb.ConnectionType) (string, error) {
	switch *connectionType {
	case pb.ConnectionTypePostgres, pb.ConnectionTypeMySQL, pb.ConnectionTypeMSSQL:
		return "SELECT 1", nil
	case pb.ConnectionTypeOracleDB:
		return "SELECT 1 FROM dual;", nil
	case pb.ConnectionTypeMongoDB:
		return `// Ensure verbosity is off
if (typeof noVerbose === 'function') noVerbose();
if (typeof config !== 'undefined') config.verbosity = 0;
printjson(db.runCommand({ping:1}));`, nil
	case pb.ConnectionTypeDynamoDB:
		return "aws dynamodb list-tables --max-items 1 --output json", nil
	case pb.ConnectionTypeCloudWatch:
		return "aws logs describe-log-groups --output json", nil
	default:
		return "", fmt.Errorf("unsupported connection type: %v", connectionType.String())
	}
}

func testConnection(ctx *storagev2.Context, bearerToken string, conn *models.Connection) error {
	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.GetOrgID(),
		ConnectionName: conn.Name,
		BearerToken:    bearerToken,
		UserAgent:      "webapp.editor.testconnection",
		Verb:           pb.ClientVerbPlainExec,
	})

	if err != nil {
		return fmt.Errorf("failed creating client: %w", err)
	}

	defer client.Close()

	currentConnectionType := pb.ToConnectionType(conn.Type, conn.SubType.String)
	command, err := getScriptsForTestConnection(&currentConnectionType)
	if err != nil {
		return err
	}

	outcome := client.Run([]byte(command), nil)
	if outcome.ExitCode != 0 {
		return fmt.Errorf("failed issuing test command, output=%v", outcome.Output)
	}

	// Custom handling for OracleDB, as it returns always exit code 0 even if the command fails
	if currentConnectionType == pb.ConnectionTypeOracleDB {
		normalizedOutput := strings.ToLower(strings.TrimSpace(outcome.Output))

		if strings.HasPrefix(normalizedOutput, "error") || strings.HasPrefix(normalizedOutput, "sp2-") {
			return fmt.Errorf("failed issuing test command, output=%v", outcome.Output)
		}
	}

	log.Infof("successful connection test for connection '%s': %v", conn.Name, outcome.Output)

	return nil
}
