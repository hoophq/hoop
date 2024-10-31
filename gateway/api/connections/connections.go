package apiconnections

import (
	"database/sql"
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
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

// CreateConnection
//
//	@Summary				Create Connection
//	@description.markdown	api-connection
//	@Tags					Core
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
	req.Status = pgrest.ConnectionStatusOffline
	if streamclient.IsAgentOnline(streamtypes.NewStreamID(req.AgentId, "")) {
		req.Status = pgrest.ConnectionStatusOnline
	}

	err = models.UpsertConnection(&models.Connection{
		ID:                 req.ID,
		OrgID:              ctx.OrgID,
		AgentID:            sql.NullString{String: req.AgentId, Valid: true},
		Name:               req.Name,
		Command:            req.Command,
		Type:               string(req.Type),
		SubType:            sql.NullString{String: req.SubType, Valid: true},
		Envs:               coerceToMapString(req.Secrets),
		Status:             req.Status,
		ManagedBy:          sql.NullString{},
		Tags:               req.Tags,
		AccessModeRunbooks: req.AccessModeRunbooks,
		AccessModeExec:     req.AccessModeExec,
		AccessModeConnect:  req.AccessModeConnect,
		AccessSchema:       req.AccessSchema,
		GuardRailRules:     req.GuardRailRules,
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
//	@Tags			Core
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
	if conn.ManagedBy != nil {
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
	req.Status = conn.Status
	err = models.UpsertConnection(&models.Connection{
		ID:                 conn.ID,
		OrgID:              conn.OrgID,
		AgentID:            sql.NullString{String: req.AgentId, Valid: true},
		Name:               conn.Name,
		Command:            req.Command,
		Type:               req.Type,
		SubType:            sql.NullString{String: req.SubType, Valid: true},
		Envs:               coerceToMapString(req.Secrets),
		Status:             conn.Status,
		ManagedBy:          sql.NullString{},
		Tags:               req.Tags,
		AccessModeRunbooks: req.AccessModeRunbooks,
		AccessModeExec:     req.AccessModeExec,
		AccessModeConnect:  req.AccessModeConnect,
		AccessSchema:       req.AccessSchema,
		GuardRailRules:     req.GuardRailRules,
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
//	@Tags			Core
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
//	@Tags			Core
//	@Produce		json
//	@Param			agent_id	query		string	false	"Filter by agent id"	Format(uuid)
//	@Param			tags		query		array	false	"Filter by tags"		Format(array)
//	@Param			type		query		string	false	"Filter by type"		Format(string)
//	@Param			subtype		query		string	false	"Filter by subtype"		Format(string)
//	@Param			managed_by	query		string	false	"Filter by managed by"	Format(string)
//	@Success		200			{array}		openapi.Connection
//	@Failure		422,500		{object}	openapi.HTTPError
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
			responseConnList = append(responseConnList, openapi.Connection{
				ID:                 conn.ID,
				Name:               conn.Name,
				Command:            conn.Command,
				Type:               conn.Type,
				SubType:            conn.SubType.String,
				Secrets:            coerceToAnyMap(conn.Envs),
				AgentId:            conn.AgentID.String,
				Status:             conn.Status,
				Reviewers:          conn.Reviewers,
				RedactEnabled:      conn.RedactEnabled,
				RedactTypes:        conn.RedactTypes,
				ManagedBy:          managedBy,
				Tags:               conn.Tags,
				AccessModeRunbooks: conn.AccessModeRunbooks,
				AccessModeExec:     conn.AccessModeExec,
				AccessModeConnect:  conn.AccessModeConnect,
				AccessSchema:       conn.AccessSchema,
				GuardRailRules:     conn.GuardRailRules,
			})
		}

	}
	c.JSON(http.StatusOK, responseConnList)
}

// Get Connection
//
//	@Summary		Get Connection
//	@Description	Get resource by name or id
//	@Tags			Core
//	@Param			nameOrID	path	string	true	"Name or UUID of the connection"
//	@Produce		json
//	@Success		200		{object}	openapi.Connection
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	conn, err := models.GetConnectionByNameOrID(ctx.OrgID, c.Param("nameOrID"))
	// conn, err := pgconnections.New().FetchOneByNameOrID(ctx, c.Param("nameOrID"))
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
	c.JSON(http.StatusOK, openapi.Connection{
		ID:                 conn.ID,
		Name:               conn.Name,
		Command:            conn.Command,
		Type:               conn.Type,
		SubType:            conn.SubType.String,
		Secrets:            coerceToAnyMap(conn.Envs),
		AgentId:            conn.AgentID.String,
		Status:             conn.Status,
		Reviewers:          conn.Reviewers,
		RedactEnabled:      conn.RedactEnabled,
		RedactTypes:        conn.RedactTypes,
		ManagedBy:          managedBy,
		Tags:               conn.Tags,
		AccessModeRunbooks: conn.AccessModeRunbooks,
		AccessModeExec:     conn.AccessModeExec,
		AccessModeConnect:  conn.AccessModeConnect,
		AccessSchema:       conn.AccessSchema,
		GuardRailRules:     conn.GuardRailRules,
	})
}

// FetchByName fetches a connection based in access control rules
func FetchByName(ctx pgrest.Context, connectionName string) (*models.Connection, error) {
	// conn, err := pgconnections.New().FetchOneByNameOrID(ctx, connectionName)
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
