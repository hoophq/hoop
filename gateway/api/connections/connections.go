package apiconnections

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
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
	})
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
//	@Description	Update a connection resource.
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
//	@Param			nameOrID		path		string				true	"The name or ID of the resource"
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
//	@Param			agent_id		query		string	false	"Filter by agent id"																	Format(uuid)
//	@Param			tags			query		string	false	"DEPRECATED: Filter by tags, separated by comma"										Format(string)
//	@Param			tag_selector	query		string	false	"Selector tags to fo filter on, supports '=' and '!=' (e.g. key1=value1,key2=value2)"	Format(string)
//	@Param			search			query		string	false	"Search by name, type, subtype, resource name or status"						Format(string)
//	@Param			type			query		string	false	"Filter by type"																		Format(string)
//	@Param			subtype			query		string	false	"Filter by subtype"																		Format(string)
//	@Param			managed_by		query		string	false	"Filter by managed by"																	Format(string)
//	@Param			resource_name		query		string	false	"Filter by resource name"																	Format(string)
//	@Param			connection_ids	query		string	false	"Filter by specific connection IDs, separated by comma"									Format(string)
//	@Param			page_size		query		int		false	"Maximum number of items to return (1-100). When provided, enables pagination"			Format(int)
//	@Param			page			query		int		false	"Page number (1-based). When provided, enables pagination"								Format(int)
//	@Success		200				{object}		openapi.PaginatedResponse[openapi.Connection]	"Returns Connection objects paginated when using pagination"
//	@Success		200				{array}		openapi.Connection	"Returns array of Connection objects"
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
	}
}

// ListDatabases return a list of databases for a given connection
//
//	@Summary		List Databases
//	@Description	List all available databases for a database connection
//	@Tags			Connections
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the connection"
//	@Success		200			{object}	openapi.ConnectionDatabaseListResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/databases [get]
func ListDatabases(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connNameOrID := c.Param("nameOrID")

	conn, err := models.GetConnectionByNameOrID(ctx, connNameOrID)
	if err != nil {
		log.Errorf("failed fetching connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
		return
	}

	if conn.Type != "database" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "connection is not a database type"})
		return
	}

	currentConnectionType := pb.ToConnectionType(conn.Type, conn.SubType.String)
	var script string
	switch currentConnectionType {
	case pb.ConnectionTypePostgres:
		script = `
SELECT datname as database_name
FROM pg_database
WHERE datistemplate = false
  AND datname != 'postgres'
	AND datname != 'rdsadmin'
ORDER BY datname;`

	case pb.ConnectionTypeMongoDB:
		script = `
// if (typeof noVerbose === 'function') noVerbose();
// if (typeof config !== 'undefined') config.verbosity = 0;

var dbs = db.adminCommand('listDatabases');
var result = [];
dbs.databases.forEach(function(database) {
	result.push({
					"database_name": database.name
	});
});
print(JSON.stringify(result));`
	case pb.ConnectionTypeMySQL:
		script = `
SELECT schema_name AS database_name
FROM information_schema.schemata
ORDER BY schema_name;`

	default:
		log.Warnf("unsupported database type: %v", currentConnectionType)
		c.JSON(http.StatusBadRequest, gin.H{"message": "unsupported database type"})
		return
	}

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	client, err := clientexec.New(&clientexec.Options{
		OrgID:                     ctx.GetOrgID(),
		ConnectionName:            conn.Name,
		ConnectionCommandOverride: getConnectionCommandOverride(currentConnectionType, conn.Command),
		BearerToken:               apiroutes.GetAccessTokenFromRequest(c),
		UserAgent:                 userAgent,
		// it sets the execution to perform plain executions
		Verb: pb.ClientVerbPlainExec,
	})
	if err != nil {
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	respCh := make(chan *clientexec.Response)
	go func() {
		defer func() { close(respCh); client.Close() }()
		select {
		case respCh <- client.Run([]byte(script), nil):
		default:
		}
	}()

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*50)
	defer cancelFn()
	select {
	case outcome := <-respCh:
		if outcome.ExitCode != 0 {
			log.Errorf("failed issuing plain exec: %s, output=%v", outcome.String(), outcome.Output)
			c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("command failed: %s", outcome.Output)})
			return
		}

		var databases []string
		var err error

		if currentConnectionType == pb.ConnectionTypeMongoDB {
			var result []map[string]any
			if output := cleanMongoOutput(outcome.Output); output != "" {
				if err := json.Unmarshal([]byte(output), &result); err != nil {
					log.Errorf("failed parsing mongo response: %v", err)
					c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to parse MongoDB response: %v", err)})
					return
				}
			}
			for _, db := range result {
				if dbName, ok := db["database_name"].(string); ok {
					databases = append(databases, dbName)
				}
			}
		} else {
			databases, err = parseDatabaseCommandOutput(outcome.Output)
			if err != nil {
				log.Errorf("failed parsing command output response: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to parse response: %v", err)})
				return
			}
		}

		c.JSON(http.StatusOK, openapi.ConnectionDatabaseListResponse{
			Databases: databases,
		})
	case <-timeoutCtx.Done():
		client.Close()
		log.Infof("runexec timeout (50s), it will return async")
		c.JSON(http.StatusBadRequest, gin.H{"message": "Request timed out"})
	}
}

// ListTables returns only the tables of a database without column details
//
//	@Summary		List Database Tables
//	@Description	List tables from a database without column details
//	@Tags			Connections
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the connection"
//	@Param			database	query		string	true	"Name of the database"
//	@Param			schema		query		string	false	"Name of the schema (optional - if not provided, returns tables from all schemas)"
//	@Success		200			{object}	openapi.TablesResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/tables [get]
func ListTables(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connNameOrID := c.Param("nameOrID")
	dbName := c.Query("database")
	schemaName := c.Query("schema")

	conn, err := models.GetConnectionByNameOrID(ctx, connNameOrID)
	if err != nil {
		log.Errorf("failed fetching connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
		return
	}

	isDatabaseConnection := conn.Type == "database" ||
		(conn.Type == "custom" && conn.SubType.String == "dynamodb") ||
		(conn.Type == "custom" && conn.SubType.String == "cloudwatch")
	if !isDatabaseConnection {
		c.JSON(http.StatusBadRequest, gin.H{"message": "connection is not a database type"})
		return
	}

	currentConnectionType := pb.ToConnectionType(conn.Type, conn.SubType.String)

	// Verify if dbName is needed (except for DynamoDB)
	needsDbName := currentConnectionType == pb.ConnectionTypePostgres ||
		currentConnectionType == pb.ConnectionTypeMySQL ||
		currentConnectionType == pb.ConnectionTypeMongoDB

	// DynamoDB doesn't need dbName
	if conn.Type == "custom" && conn.SubType.String == "dynamodb" ||
		conn.Type == "custom" && conn.SubType.String == "cloudwatch" {
		needsDbName = false
	}

	// For database types that require dbName
	if needsDbName {
		// Check if provided
		if dbName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"message": "database parameter is required for this database type"})
			return
		}

		// Validate format
		if err := validateDatabaseName(dbName); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
			return
		}
	}

	script := getTablesQuery(currentConnectionType, dbName)
	if script == "" {
		// Check for DynamoDB
		if conn.Type == "custom" && conn.SubType.String == "dynamodb" {
			script = `aws dynamodb list-tables --output json`
		}
	}

	if script == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "unsupported database type"})
		return
	}

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	client, err := clientexec.New(&clientexec.Options{
		OrgID:                     ctx.GetOrgID(),
		ConnectionName:            conn.Name,
		ConnectionCommandOverride: getConnectionCommandOverride(currentConnectionType, conn.Command),
		BearerToken:               apiroutes.GetAccessTokenFromRequest(c),
		UserAgent:                 userAgent,
		Verb:                      pb.ClientVerbPlainExec,
	})
	if err != nil {
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	respCh := make(chan *clientexec.Response)
	go func() {
		defer func() { close(respCh); client.Close() }()
		select {
		case respCh <- client.Run([]byte(script), nil):
		default:
		}
	}()

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*30)
	defer cancelFn()
	select {
	case outcome := <-respCh:
		if outcome.ExitCode != 0 {
			log.Warnf("failed issuing plain exec: %s, output=%v", outcome.String(), outcome.Output)
			c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("failed to list tables: %s", outcome.Output)})
			return
		}

		var response openapi.TablesResponse

		// Check for DynamoDB
		if conn.Type == "custom" && conn.SubType.String == "dynamodb" {
			// Special parsing for DynamoDB
			tables, err := parseDynamoDBTables(outcome.Output)
			if err != nil {
				log.Errorf("failed parsing DynamoDB response: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to parse DynamoDB response: %v", err)})
				return
			}
			response = tables
		} else if currentConnectionType == pb.ConnectionTypeCloudWatch {
			// Special parsing for CloudWatch
			tables, err := parseCloudWatchTables(outcome.Output)
			if err != nil {
				log.Errorf("failed parsing CloudWatch response: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to parse CloudWatch response: %v", err)})
				return
			}
			response = tables
		} else if currentConnectionType == pb.ConnectionTypeMongoDB {
			// Parse MongoDB output
			tables, err := parseMongoDBTables(outcome.Output)
			if err != nil {
				log.Errorf("failed parsing mongo response: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to parse MongoDB response: %v", err)})
				return
			}
			response = tables
		} else {
			// Parse SQL output
			tables, err := parseSQLTables(outcome.Output, currentConnectionType)
			if err != nil {
				log.Errorf("failed parsing SQL response: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to parse SQL response: %v", err)})
				return
			}

			// If a specific schema was requested, filter the results
			if schemaName != "" {
				filteredSchemas := []openapi.SchemaInfo{}
				for _, schema := range tables.Schemas {
					if schema.Name == schemaName {
						filteredSchemas = append(filteredSchemas, schema)
						break
					}
				}
				tables.Schemas = filteredSchemas
			}

			response = tables
		}

		c.JSON(http.StatusOK, response)

	case <-timeoutCtx.Done():
		client.Close()
		log.Warnf("timeout (30s) obtaining tables for database '%s' using connection '%s'", dbName, conn.Name)
		c.JSON(http.StatusBadRequest, gin.H{
			"message":    fmt.Sprintf("Request timed out (30s) while fetching tables for database '%s'", dbName),
			"connection": conn.Name,
			"database":   dbName,
			"timeout":    "30s",
		})
	}
}

// GetTableColumns returns the columns of a specific table
//
//	@Summary		Get Table Columns
//	@Description	Get columns from a specific table
//	@Tags			Connections
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the connection"
//	@Param			database	query		string	true	"Name of the database"
//	@Param			table		query		string	true	"Name of the table"
//	@Param			schema		query		string	false	"Name of the schema (optional - for PostgreSQL default is 'public', for others defaults to database name)"
//	@Success		200			{object}	openapi.ColumnsResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/columns [get]
func GetTableColumns(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connNameOrID := c.Param("nameOrID")
	dbName := c.Query("database")
	tableName := c.Query("table")
	schemaName := c.Query("schema")

	if tableName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "table parameter is required"})
		return
	}

	conn, err := models.GetConnectionByNameOrID(ctx, connNameOrID)
	if err != nil {
		log.Errorf("failed fetching connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
		return
	}

	isDatabaseConnection := conn.Type == "database" ||
		(conn.Type == "custom" && conn.SubType.String == "dynamodb")
	if !isDatabaseConnection {
		c.JSON(http.StatusBadRequest, gin.H{"message": "connection is not a database type"})
		return
	}

	currentConnectionType := pb.ToConnectionType(conn.Type, conn.SubType.String)

	// Verify if dbName is needed (except for DynamoDB)
	needsDbName := currentConnectionType == pb.ConnectionTypePostgres ||
		currentConnectionType == pb.ConnectionTypeMySQL ||
		currentConnectionType == pb.ConnectionTypeMongoDB

	// DynamoDB doesn't need dbName
	if currentConnectionType == pb.ConnectionTypeDynamoDB {
		needsDbName = false
	}

	// For database types that require dbName
	if needsDbName {
		// Check if provided
		if dbName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"message": "database parameter is required for this database type"})
			return
		}

		// Validate format
		if err := validateDatabaseName(dbName); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
			return
		}
	}

	if schemaName == "" {
		schemaName = dbName
		if currentConnectionType == pb.ConnectionTypePostgres {
			schemaName = "public"
		}
	}

	script := getColumnsQuery(currentConnectionType, dbName, tableName, schemaName)
	if script == "" {
		// Check for DynamoDB
		if currentConnectionType == pb.ConnectionTypeDynamoDB {
			script = fmt.Sprintf(`aws dynamodb describe-table --table-name %s --output json`, tableName)
		}
	}

	if script == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "unsupported database type"})
		return
	}

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.GetOrgID(),
		ConnectionName: conn.Name,
		BearerToken:    apiroutes.GetAccessTokenFromRequest(c),
		UserAgent:      userAgent,
		Verb:           pb.ClientVerbPlainExec,
	})
	if err != nil {
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	respCh := make(chan *clientexec.Response)
	go func() {
		defer func() { close(respCh); client.Close() }()
		select {
		case respCh <- client.Run([]byte(script), nil):
		default:
		}
	}()

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*30)
	defer cancelFn()
	select {
	case outcome := <-respCh:
		if outcome.ExitCode != 0 {
			log.Warnf("failed issuing plain exec: %s, output=%v", outcome.String(), outcome.Output)
			c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("failed to get columns: %s", outcome.Output)})
			return
		}

		response := openapi.ColumnsResponse{Columns: []openapi.ConnectionColumn{}}

		// Check for DynamoDB
		if currentConnectionType == pb.ConnectionTypeDynamoDB {
			// Special parsing for DynamoDB
			columns, err := parseDynamoDBColumns(outcome.Output)
			if err != nil {
				log.Errorf("failed parsing DynamoDB response: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to parse DynamoDB response: %v", err)})
				return
			}
			response.Columns = columns
		} else if currentConnectionType == pb.ConnectionTypeMongoDB {
			// Parse MongoDB output
			columns, err := parseMongoDBColumns(outcome.Output)
			if err != nil {
				log.Errorf("failed parsing mongo response: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to parse MongoDB response: %v", err)})
				return
			}
			response.Columns = columns
		} else {
			// Parse SQL output
			columns, err := parseSQLColumns(outcome.Output, currentConnectionType)
			if err != nil {
				log.Errorf("failed parsing SQL response: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to parse SQL response: %v", err)})
				return
			}
			response.Columns = columns
		}

		c.JSON(http.StatusOK, response)

	case <-timeoutCtx.Done():
		client.Close()
		log.Warnf("timeout (30s) obtaining columns for table '%s' in database '%s' using connection '%s'", tableName, dbName, conn.Name)
		c.JSON(http.StatusBadRequest, gin.H{
			"message":    fmt.Sprintf("Request timed out (30s) while fetching columns for table '%s'", tableName),
			"connection": conn.Name,
			"database":   dbName,
			"table":      tableName,
			"timeout":    "30s",
		})
	}
}

// Test Connection
//
//	@Summary		Test Connection
//	@Description	Test resource by name or id (only for database connections, it will attempt a simple ping).
//	@Tags				Connections
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
	if currentConnectionType == pb.ConnectionTypeOracleDB && strings.HasPrefix(strings.ToLower(outcome.Output), "error") {
		return fmt.Errorf("failed issuing test command, output=%v", outcome.Output)
	}

	log.Infof("successful connection test for connection '%s': %v", conn.Name, outcome.Output)

	return nil
}
