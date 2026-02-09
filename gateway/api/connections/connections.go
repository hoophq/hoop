package apiconnections

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/audit"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/transport/connectionrequests"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
)

type Review struct {
	ApprovalGroups []string `json:"groups"`
}

// CreateConnection
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
	existingConn, err := models.GetConnectionByNameOrID(ctx, req.Name)
	if err != nil {
		log.Errorf("failed fetching existing connection, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
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

	resp, err := models.UpsertConnection(ctx, &models.Connection{
		ID:                  req.ID,
		OrgID:               ctx.OrgID,
		ResourceName:        req.ResourceName,
		AgentID:             sql.NullString{String: req.AgentId, Valid: true},
		Name:                req.Name,
		Command:             req.Command,
		Type:                req.Type,
		SubType:             sql.NullString{String: req.SubType, Valid: true},
		Envs:                CoerceToMapString(req.Secrets),
		Status:              req.Status,
		ManagedBy:           sql.NullString{},
		Tags:                req.Tags,
		AccessModeRunbooks:  req.AccessModeRunbooks,
		AccessModeExec:      req.AccessModeExec,
		AccessModeConnect:   req.AccessModeConnect,
		AccessSchema:        req.AccessSchema,
		Reviewers:           req.Reviewers,
		RedactTypes:         req.RedactTypes,
		GuardRailRules:      req.GuardRailRules,
		JiraIssueTemplateID: sql.NullString{String: req.JiraIssueTemplateID, Valid: true},
		ConnectionTags:      req.ConnectionTags,
		ForceApproveGroups:  req.ForceApproveGroups,
		AccessMaxDuration:   req.AccessMaxDuration,
		MinReviewApprovals:  req.MinReviewApprovals,
	})
	resourceID := ""
	if resp != nil {
		resourceID = resp.ID
	}
	audit.LogFromContextErr(c, audit.ResourceConnection, audit.ActionCreate, resourceID, req.Name, payloadConnectionCreate(req.Name, req.Type, req.AgentId), err)
	if err != nil {
		log.Errorf("failed creating connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, toOpenApi(resp))
}

// UpdateConnection
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
		log.Errorf("failed fetching connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
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
	setConnectionDefaults(&req)

	// immutable fields
	req.ID = conn.ID
	req.Name = conn.Name
	req.Status = models.ConnectionStatusOffline
	if streamclient.IsAgentOnline(streamtypes.NewStreamID(req.AgentId, "")) {
		req.Status = models.ConnectionStatusOnline
	}

	resp, err := models.UpsertConnection(ctx, &models.Connection{
		ID:                  conn.ID,
		OrgID:               conn.OrgID,
		ResourceName:        req.ResourceName,
		AgentID:             sql.NullString{String: req.AgentId, Valid: true},
		Name:                conn.Name,
		Command:             req.Command,
		Type:                req.Type,
		SubType:             sql.NullString{String: req.SubType, Valid: true},
		Envs:                CoerceToMapString(req.Secrets),
		Status:              req.Status,
		ManagedBy:           sql.NullString{},
		Tags:                req.Tags,
		AccessModeRunbooks:  req.AccessModeRunbooks,
		AccessModeExec:      req.AccessModeExec,
		AccessModeConnect:   req.AccessModeConnect,
		AccessSchema:        req.AccessSchema,
		Reviewers:           req.Reviewers,
		RedactTypes:         req.RedactTypes,
		GuardRailRules:      req.GuardRailRules,
		JiraIssueTemplateID: sql.NullString{String: req.JiraIssueTemplateID, Valid: true},
		ConnectionTags:      req.ConnectionTags,
		ForceApproveGroups:  req.ForceApproveGroups,
		AccessMaxDuration:   req.AccessMaxDuration,
		MinReviewApprovals:  req.MinReviewApprovals,
	})
	if err != nil {
		switch err.(type) {
		case *models.ErrNotFoundGuardRailRules:
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		default:
			log.Errorf("failed updating connection, err=%v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, toOpenApi(resp))
}

// PatchConnection
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
		log.Errorf("failed fetching connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
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
		conn.Envs = CoerceToMapString(*req.Secrets)
	}
	if req.AgentId != nil {
		conn.AgentID = sql.NullString{String: *req.AgentId, Valid: *req.AgentId != ""}
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

	// Update status
	conn.Status = models.ConnectionStatusOffline
	if streamclient.IsAgentOnline(streamtypes.NewStreamID(conn.AgentID.String, "")) {
		conn.Status = models.ConnectionStatusOnline
	}

	resp, err := models.UpsertConnection(ctx, conn)
	audit.LogFromContextErr(c, audit.ResourceConnection, audit.ActionUpdate, conn.ID, conn.Name, payloadConnectionUpdate(conn.Name, conn.Type), err)
	if err != nil {
		switch err.(type) {
		case *models.ErrNotFoundGuardRailRules:
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		default:
			log.Errorf("failed patching connection, err=%v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, toOpenApi(resp))
}

// DeleteConnection
//
//	@Summary		Delete Connection
//	@Description	Delete a connection resource.
//	@Tags			Connections
//	@Produce		json
//	@Param			name	path	string	true	"The name of the resource"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/connections/{name} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connName := c.Param("name")
	if connName == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "missing connection name"})
		return
	}
	err := models.DeleteConnection(ctx.OrgID, connName)
	audit.LogFromContextErr(c, audit.ResourceConnection, audit.ActionDelete, connName, connName, nil, err)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
	case nil:
		connectionrequests.InvalidateSyncCache(ctx.OrgID, connName)
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		log.Errorf("failed removing connection %v, err=%v", connName, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed removing connection"})
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
			log.Errorf("failed listing connections with pagination, reason=%v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
			return
		}

		responseConnList := make([]openapi.Connection, len(connList))
		for i, conn := range connList {
			// it should return empty to avoid leaking sensitive content
			// in the future we plan to know which entry is sensitive or not
			conn.Envs = map[string]string{}
			responseConnList[i] = toOpenApi(&conn)
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
		log.Errorf("failed listing connections, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	responseConnList := []openapi.Connection{}
	for _, conn := range connList {
		// it should return empty to avoid leaking sensitive content
		// in the future we plan to know which entry is sensitive or not
		conn.Envs = map[string]string{}
		responseConnList = append(responseConnList, toOpenApi(&conn))

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
		log.Errorf("failed fetching connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	c.JSON(http.StatusOK, toOpenApi(conn))
}

func toOpenApi(conn *models.Connection) openapi.Connection {
	var managedBy *string
	if conn.ManagedBy.Valid {
		managedBy = &conn.ManagedBy.String
	}
	defaultDB, _ := base64.StdEncoding.DecodeString(conn.Envs["envvar:DB"])
	if len(defaultDB) == 0 {
		defaultDB = []byte(``)
	}

	return openapi.Connection{
		ID:                  conn.ID,
		Name:                conn.Name,
		ResourceName:        conn.ResourceName,
		Command:             conn.Command,
		Type:                conn.Type,
		SubType:             conn.SubType.String,
		Secrets:             coerceToAnyMap(conn.Envs),
		DefaultDatabase:     string(defaultDB),
		AgentId:             conn.AgentID.String,
		Status:              conn.Status,
		Reviewers:           conn.Reviewers,
		RedactEnabled:       conn.RedactEnabled,
		RedactTypes:         conn.RedactTypes,
		ManagedBy:           managedBy,
		Tags:                conn.Tags,
		ConnectionTags:      conn.ConnectionTags,
		AccessModeRunbooks:  conn.AccessModeRunbooks,
		AccessModeExec:      conn.AccessModeExec,
		AccessModeConnect:   conn.AccessModeConnect,
		AccessSchema:        conn.AccessSchema,
		GuardRailRules:      conn.GuardRailRules,
		JiraIssueTemplateID: conn.JiraIssueTemplateID.String,
		ForceApproveGroups:  conn.ForceApproveGroups,
		AccessMaxDuration:   conn.AccessMaxDuration,
		MinReviewApprovals:  conn.MinReviewApprovals,
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
		log.Errorf("failed fetching connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
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

func payloadConnectionCreate(name, connType, agentID string) audit.PayloadFn {
	return func() map[string]any {
		return map[string]any{"name": name, "type": connType, "agent_id": agentID}
	}
}

func payloadConnectionUpdate(name, connType string) audit.PayloadFn {
	return func() map[string]any {
		return map[string]any{"name": name, "type": connType}
	}
}
