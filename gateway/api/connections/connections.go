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
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgplugins "github.com/hoophq/hoop/gateway/pgrest/plugins"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/hoophq/hoop/gateway/transport/connectionrequests"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
)

type Review struct {
	ApprovalGroups []string `json:"groups"`
}

// Estruturas para respostas dos novos endpoints
type TablesResponse struct {
	Schemas []struct {
		Name   string   `json:"name"`
		Tables []string `json:"tables"`
	} `json:"schemas"`
}

type ColumnsResponse struct {
	Columns []openapi.ConnectionColumn `json:"columns"`
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
	existingConn, err := models.GetConnectionByNameOrID(ctx.OrgID, req.Name)
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

	err = models.UpsertConnection(&models.Connection{
		ID:                  req.ID,
		OrgID:               ctx.OrgID,
		AgentID:             sql.NullString{String: req.AgentId, Valid: true},
		Name:                req.Name,
		Command:             req.Command,
		Type:                string(req.Type),
		SubType:             sql.NullString{String: req.SubType, Valid: true},
		Envs:                coerceToMapString(req.Secrets),
		Status:              req.Status,
		ManagedBy:           sql.NullString{},
		Tags:                req.Tags,
		AccessModeRunbooks:  req.AccessModeRunbooks,
		AccessModeExec:      req.AccessModeExec,
		AccessModeConnect:   req.AccessModeConnect,
		AccessSchema:        req.AccessSchema,
		GuardRailRules:      req.GuardRailRules,
		JiraIssueTemplateID: sql.NullString{String: req.JiraIssueTemplateID, Valid: true},
		ConnectionTags:      req.ConnectionTags,
	})
	if err != nil {
		log.Errorf("failed creating connection, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	pgplugins.EnableDefaultPlugins(ctx, req.ID, req.Name, pgplugins.DefaultPluginNames)
	// configure review and dlp plugins (best-effort)
	for _, pluginName := range []string{plugintypes.PluginReviewName, plugintypes.PluginDLPName} {
		// skip configuring redact if the client doesn't set redact_enabled
		// it maintain compatibility with old clients since we enable dlp with default redact types
		if pluginName == plugintypes.PluginDLPName && !req.RedactEnabled {
			continue
		}
		pluginConnConfig := req.Reviewers
		if pluginName == plugintypes.PluginDLPName {
			pluginConnConfig = req.RedactTypes
		}
		pgplugins.UpsertPluginConnection(ctx, pluginName, &types.PluginConnection{
			ID:           uuid.NewString(),
			ConnectionID: req.ID,
			Name:         req.Name,
			Config:       pluginConnConfig,
		})
	}
	c.JSON(http.StatusCreated, req)
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
	conn, err := models.GetConnectionByNameOrID(ctx.OrgID, connNameOrID)
	if err != nil {
		log.Errorf("failed fetching connection, err=%v", err)
		sentry.CaptureException(err)
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
	err = models.UpsertConnection(&models.Connection{
		ID:                  conn.ID,
		OrgID:               conn.OrgID,
		AgentID:             sql.NullString{String: req.AgentId, Valid: true},
		Name:                conn.Name,
		Command:             req.Command,
		Type:                req.Type,
		SubType:             sql.NullString{String: req.SubType, Valid: true},
		Envs:                coerceToMapString(req.Secrets),
		Status:              req.Status,
		ManagedBy:           sql.NullString{},
		Tags:                req.Tags,
		AccessModeRunbooks:  req.AccessModeRunbooks,
		AccessModeExec:      req.AccessModeExec,
		AccessModeConnect:   req.AccessModeConnect,
		AccessSchema:        req.AccessSchema,
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
			sentry.CaptureException(err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		}
		return
	}
	connectionrequests.InvalidateSyncCache(ctx.OrgID, conn.Name)
	// configure review and dlp plugins (best-effort)
	for _, pluginName := range []string{plugintypes.PluginReviewName, plugintypes.PluginDLPName} {
		// skip configuring redact if the client doesn't set redact_enabled
		// it maintain compatibility with old clients since we enable dlp with default redact types
		if pluginName == plugintypes.PluginDLPName && !req.RedactEnabled {
			continue
		}
		pluginConnConfig := req.Reviewers
		if pluginName == plugintypes.PluginDLPName {
			pluginConnConfig = req.RedactTypes
		}
		pgplugins.UpsertPluginConnection(ctx, pluginName, &types.PluginConnection{
			ID:           uuid.NewString(),
			ConnectionID: conn.ID,
			Name:         req.Name,
			Config:       pluginConnConfig,
		})
	}
	c.JSON(http.StatusOK, req)
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
	case pgrest.ErrNotFound:
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
//	@Param			type			query		string	false	"Filter by type"																		Format(string)
//	@Param			subtype			query		string	false	"Filter by subtype"																		Format(string)
//	@Param			managed_by		query		string	false	"Filter by managed by"																	Format(string)
//	@Success		200				{array}		openapi.Connection
//	@Failure		422,500			{object}	openapi.HTTPError
//	@Router			/connections [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	filterOpts, err := validateListOptions(c.Request.URL.Query())
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	connList, err := models.ListConnections(ctx.OrgID, filterOpts)
	if err != nil {
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	allowedFn, err := accessControlAllowed(ctx)
	if err != nil {
		log.Errorf("failed validating connection access control, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	responseConnList := []openapi.Connection{}
	for _, conn := range connList {
		if allowedFn(conn.Name) {
			var managedBy *string
			if conn.ManagedBy.Valid {
				managedBy = &conn.ManagedBy.String
			}
			defaultDB, _ := base64.StdEncoding.DecodeString(conn.Envs["envvar:DB"])
			if len(defaultDB) == 0 {
				defaultDB = []byte(``)
			}
			responseConnList = append(responseConnList, openapi.Connection{
				ID:      conn.ID,
				Name:    conn.Name,
				Command: conn.Command,
				Type:    conn.Type,
				SubType: conn.SubType.String,
				// it should return empty to avoid leaking sensitive content
				// in the future we plan to know which entry is sensitive or not
				Secrets:             nil,
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
			})
		}

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
	conn, err := models.GetConnectionByNameOrID(ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		log.Errorf("failed fetching connection, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	allowedFn, err := accessControlAllowed(ctx)
	if err != nil {
		log.Errorf("failed validating connection access control, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if conn == nil || !allowedFn(conn.Name) {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	var managedBy *string
	if conn.ManagedBy.Valid {
		managedBy = &conn.ManagedBy.String
	}
	defaultDB, _ := base64.StdEncoding.DecodeString(conn.Envs["envvar:DB"])
	if len(defaultDB) == 0 {
		defaultDB = []byte(``)
	}
	c.JSON(http.StatusOK, openapi.Connection{
		ID:                  conn.ID,
		Name:                conn.Name,
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
	})
}

// FetchByName fetches a connection based in access control rules
func FetchByName(ctx pgrest.Context, connectionName string) (*models.Connection, error) {
	conn, err := models.GetConnectionByNameOrID(ctx.GetOrgID(), connectionName)
	if err != nil {
		return nil, err
	}
	allowedFn, err := accessControlAllowed(ctx)
	if err != nil {
		return nil, err
	}
	if conn == nil || !allowedFn(conn.Name) {
		return nil, nil
	}
	return conn, nil
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

	conn, err := FetchByName(ctx, connNameOrID)
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

	default:
		log.Warnf("unsupported database type: %v", currentConnectionType)
		c.JSON(http.StatusBadRequest, gin.H{"message": "unsupported database type"})
		return
	}

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.GetOrgID(),
		ConnectionName: conn.Name,
		BearerToken:    getAccessToken(c),
		UserAgent:      userAgent,
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

// GetDatabaseSchema return detailed schema information including tables, views, columns and indexes
//
//	@Summary		Get Database Schema
//	@Description	Get detailed schema information including tables, views, columns and indexes
//	@Tags			Connections
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the connection"
//	@Param			database	path		string	true	"Name of the database"
//	@Success		200			{object}	openapi.ConnectionSchemaResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/schemas [get]
func GetDatabaseSchemas(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connNameOrID := c.Param("nameOrID")
	dbName := c.Query("database")

	// Validate database name to prevent SQL injection
	if dbName != "" {
		if err := validateDatabaseName(dbName); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
			return
		}
	}

	conn, err := FetchByName(ctx, connNameOrID)
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

	if dbName == "" {
		connEnvs := conn.Envs
		switch currentConnectionType {
		case pb.ConnectionTypePostgres,
			pb.ConnectionTypeMSSQL,
			pb.ConnectionTypeMySQL:
			dbName = getEnvValue(connEnvs, "envvar:DB")
		case pb.ConnectionTypeMongoDB:
			if connStr := connEnvs["envvar:CONNECTION_STRING"]; connStr != "" {
				dbName = getMongoDBFromConnectionString(connStr)
			}
		case pb.ConnectionTypeOracleDB:
			dbName = getEnvValue(connEnvs, "envvar:SID")
		}

		if dbName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"message": "database name is required but not found in connection or query parameter"})
			return
		}
	}

	script := getSchemaQuery(currentConnectionType, dbName)
	if script == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "unsupported database type"})
		return
	}

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.GetOrgID(),
		ConnectionName: conn.Name,
		BearerToken:    getAccessToken(c),
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

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*50)
	defer cancelFn()
	select {
	case outcome := <-respCh:
		if outcome.ExitCode != 0 {
			log.Errorf("failed issuing plain exec: %s, output=%v", outcome.String(), outcome.Output)
			c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to get schema: %s", outcome.Output)})
			return
		}

		var schema openapi.ConnectionSchemaResponse
		var err error

		if currentConnectionType == pb.ConnectionTypeMongoDB {
			output := cleanMongoOutput(outcome.Output)
			schema, err = parseMongoDBSchema(output)
		} else {
			schema, err = parseSQLSchema(outcome.Output, currentConnectionType)
		}

		if err != nil {
			log.Errorf("failed parsing schema response: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to parse schema: %v", err)})
			return
		}

		if c.Request.Method == "HEAD" {
			contentLength := -1
			if jsonBody, err := json.Marshal(schema); err == nil {
				contentLength = len(jsonBody)
			}
			c.Writer.Header().Set("Content-Length", fmt.Sprintf("%v", contentLength))
			return
		}
		c.JSON(http.StatusOK, schema)

	case <-timeoutCtx.Done():
		client.Close()
		log.Infof("runexec timeout (50s), it will return async")
		c.JSON(http.StatusBadRequest, gin.H{"message": "Request timed out"})
	}
}

// ListTables retorna apenas as tabelas de um banco de dados sem detalhes das colunas
//
//	@Summary		List Database Tables
//	@Description	List tables from a database without column details
//	@Tags			Connections
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the connection"
//	@Param			database	query		string	true	"Name of the database"
//	@Success		200			{object}	TablesResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/tables [get]
func ListTables(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connNameOrID := c.Param("nameOrID")
	dbName := c.Query("database")

	conn, err := FetchByName(ctx, connNameOrID)
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

	if dbName == "" {
		connEnvs := conn.Envs
		switch currentConnectionType {
		case pb.ConnectionTypePostgres,
			pb.ConnectionTypeMSSQL,
			pb.ConnectionTypeMySQL:
			dbName = getEnvValue(connEnvs, "envvar:DB")
		case pb.ConnectionTypeMongoDB:
			if connStr := connEnvs["envvar:CONNECTION_STRING"]; connStr != "" {
				dbName = getMongoDBFromConnectionString(connStr)
			}
		case pb.ConnectionTypeOracleDB:
			dbName = getEnvValue(connEnvs, "envvar:SID")
		}

		if dbName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"message": "database name is required but not found in connection or query parameter"})
			return
		}
	}

	// Validate database name to prevent SQL injection
	if err := validateDatabaseName(dbName); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	script := getTablesQuery(currentConnectionType, dbName)
	if script == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "unsupported database type"})
		return
	}

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.GetOrgID(),
		ConnectionName: conn.Name,
		BearerToken:    getAccessToken(c),
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
			log.Errorf("failed issuing plain exec: %s, output=%v", outcome.String(), outcome.Output)
			c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to list tables: %s", outcome.Output)})
			return
		}

		response := TablesResponse{Schemas: []struct {
			Name   string   `json:"name"`
			Tables []string `json:"tables"`
		}{}}

		if currentConnectionType == pb.ConnectionTypeMongoDB {
			// Parse MongoDB output
			output := cleanMongoOutput(outcome.Output)
			if output != "" {
				var result []map[string]interface{}
				if err := json.Unmarshal([]byte(output), &result); err != nil {
					log.Errorf("failed parsing mongo response: %v", err)
					c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to parse MongoDB response: %v", err)})
					return
				}

				// Organize tables by schema
				schemaMap := make(map[string][]string)
				for _, row := range result {
					schemaName := getString(row, "schema_name")
					tableName := getString(row, "object_name")
					schemaMap[schemaName] = append(schemaMap[schemaName], tableName)
				}

				// Convert map to response structure
				for schemaName, tables := range schemaMap {
					response.Schemas = append(response.Schemas, struct {
						Name   string   `json:"name"`
						Tables []string `json:"tables"`
					}{
						Name:   schemaName,
						Tables: tables,
					})
				}
			}
		} else {
			// Parse SQL output
			lines := strings.Split(outcome.Output, "\n")
			schemaMap := make(map[string][]string)

			// Process each line (skip header)
			startLine := 1
			if currentConnectionType == pb.ConnectionTypeMSSQL {
				// Find the line with dashes for MSSQL
				for i, line := range lines {
					if strings.Contains(line, "----") {
						startLine = i + 1
						break
					}
				}
			}

			for i, line := range lines {
				line = strings.TrimSpace(line)
				if i < startLine || line == "" || strings.HasPrefix(line, "(") {
					continue
				}

				fields := strings.Split(line, "\t")
				if len(fields) < 3 {
					continue
				}

				schemaName := fields[0]
				tableName := fields[2]
				schemaMap[schemaName] = append(schemaMap[schemaName], tableName)
			}

			// Convert map to response structure
			for schemaName, tables := range schemaMap {
				response.Schemas = append(response.Schemas, struct {
					Name   string   `json:"name"`
					Tables []string `json:"tables"`
				}{
					Name:   schemaName,
					Tables: tables,
				})
			}
		}

		c.JSON(http.StatusOK, response)

	case <-timeoutCtx.Done():
		client.Close()
		log.Infof("runexec timeout (30s), it will return async")
		c.JSON(http.StatusBadRequest, gin.H{"message": "Request timed out"})
	}
}

// GetTableColumns retorna apenas as colunas de uma tabela específica
//
//	@Summary		Get Table Columns
//	@Description	Get columns from a specific table
//	@Tags			Connections
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the connection"
//	@Param			database	query		string	true	"Name of the database"
//	@Param			table		query		string	true	"Name of the table"
//	@Param			schema		query		string	true	"Name of the schema"
//	@Success		200			{object}	ColumnsResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/columns [get]
func GetTableColumns(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connNameOrID := c.Param("nameOrID")
	dbName := c.Query("database")
	tableName := c.Query("table")
	schemaName := c.Query("schema")

	// Validações
	if tableName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "table parameter is required"})
		return
	}

	conn, err := FetchByName(ctx, connNameOrID)
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

	if dbName == "" {
		connEnvs := conn.Envs
		switch currentConnectionType {
		case pb.ConnectionTypePostgres,
			pb.ConnectionTypeMSSQL,
			pb.ConnectionTypeMySQL:
			dbName = getEnvValue(connEnvs, "envvar:DB")
		case pb.ConnectionTypeMongoDB:
			if connStr := connEnvs["envvar:CONNECTION_STRING"]; connStr != "" {
				dbName = getMongoDBFromConnectionString(connStr)
			}
		case pb.ConnectionTypeOracleDB:
			dbName = getEnvValue(connEnvs, "envvar:SID")
		}

		if dbName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"message": "database name is required but not found in connection or query parameter"})
			return
		}
	}

	// Validate database name to prevent SQL injection
	if err := validateDatabaseName(dbName); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	// Se o schema não for fornecido, usa 'public' para PostgreSQL e o próprio database para outros bancos
	if schemaName == "" {
		if currentConnectionType == pb.ConnectionTypePostgres {
			schemaName = "public"
		} else {
			schemaName = dbName
		}
	}

	script := getColumnsQuery(currentConnectionType, dbName, tableName, schemaName)
	if script == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "unsupported database type"})
		return
	}

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.GetOrgID(),
		ConnectionName: conn.Name,
		BearerToken:    getAccessToken(c),
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
			log.Errorf("failed issuing plain exec: %s, output=%v", outcome.String(), outcome.Output)
			c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to get columns: %s", outcome.Output)})
			return
		}

		response := ColumnsResponse{Columns: []openapi.ConnectionColumn{}}

		if currentConnectionType == pb.ConnectionTypeMongoDB {
			// Parse MongoDB output
			output := cleanMongoOutput(outcome.Output)
			if output != "" {
				var result []map[string]interface{}
				if err := json.Unmarshal([]byte(output), &result); err != nil {
					log.Errorf("failed parsing mongo response: %v", err)
					c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed to parse MongoDB response: %v", err)})
					return
				}

				for _, row := range result {
					column := openapi.ConnectionColumn{
						Name:     getString(row, "column_name"),
						Type:     getString(row, "column_type"),
						Nullable: !getBool(row, "not_null"),
					}
					response.Columns = append(response.Columns, column)
				}
			}
		} else {
			// Parse SQL output
			lines := strings.Split(outcome.Output, "\n")

			// Process each line (skip header)
			startLine := 1
			if currentConnectionType == pb.ConnectionTypeMSSQL {
				// Find the line with dashes for MSSQL
				for i, line := range lines {
					if strings.Contains(line, "----") {
						startLine = i + 1
						break
					}
				}
			}

			for i, line := range lines {
				line = strings.TrimSpace(line)
				if i < startLine || line == "" || strings.HasPrefix(line, "(") {
					continue
				}

				fields := strings.Split(line, "\t")
				if len(fields) < 3 {
					continue
				}

				column := openapi.ConnectionColumn{
					Name:     fields[0],
					Type:     fields[1],
					Nullable: fields[2] != "t" && fields[2] != "1",
				}
				response.Columns = append(response.Columns, column)
			}
		}

		c.JSON(http.StatusOK, response)

	case <-timeoutCtx.Done():
		client.Close()
		log.Infof("runexec timeout (30s), it will return async")
		c.JSON(http.StatusBadRequest, gin.H{"message": "Request timed out"})
	}
}
