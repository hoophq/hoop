package apiconnections

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/apiutils"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	sessionapi "github.com/runopsio/hoop/gateway/api/session"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgconnections "github.com/runopsio/hoop/gateway/pgrest/connections"
	pgsession "github.com/runopsio/hoop/gateway/pgrest/session"
	"github.com/runopsio/hoop/gateway/storagev2"
	pluginstorage "github.com/runopsio/hoop/gateway/storagev2/plugin"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

type Review struct {
	ApprovalGroups []string `json:"groups"`
}

type Connection struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	IconName      string         `json:"icon_name"`
	Command       []string       `json:"command"`
	Type          string         `json:"type"`
	SubType       string         `json:"subtype"`
	Secrets       map[string]any `json:"secret"`
	AgentId       string         `json:"agent_id"`
	Reviewers     []string       `json:"reviewers"`
	RedactEnabled bool           `json:"redact_enabled"`
	RedactTypes   []string       `json:"redact_types"`
}

func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req Connection
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.Name == "" || strings.Contains(req.Name, "/") {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "connection name must not be empty or contain slash characters"})
		return
	}
	existingConn, err := pgconnections.New().FetchOneByNameOrID(ctx, req.Name)
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
	if len(req.Command) == 0 {
		switch string(req.Type) {
		case pb.ConnectionTypePostgres:
			req.Command = []string{"psql", "-A", "-F\t", "-P", "pager=off", "-h", "$HOST", "-U", "$USER", "--port=$PORT", "$DB"}
		case pb.ConnectionTypeMySQL:
			req.Command = []string{"mysql", "-h$HOST", "-u$USER", "--port=$PORT", "-D$DB"}
		case pb.ConnectionTypeMSSQL:
			req.Command = []string{
				"sqlcmd", "--exit-on-error", "--trim-spaces", "-r",
				"-S$HOST:$PORT", "-U$USER", "-d$DB", "-i/dev/stdin"}
		}
	}

	envs := make(map[string]string)
	for k, v := range req.Secrets {
		envs[k] = fmt.Sprintf("%v", v)
	}
	req.ID = uuid.NewString()
	err = pgconnections.New().Upsert(ctx, pgrest.Connection{
		ID:            req.ID,
		OrgID:         ctx.OrgID,
		AgentID:       req.AgentId,
		LegacyAgentID: req.AgentId,
		Name:          req.Name,
		Command:       req.Command,
		Type:          string(req.Type),
		SubType:       req.SubType,
		Envs:          envs,
	})
	if err != nil {
		log.Errorf("failed creating connection, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	pluginstorage.EnableDefaultPlugins(ctx, req.ID, req.Name)
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
		upsertPluginConnection(ctx, pluginName, &types.PluginConnection{
			ID:           uuid.NewString(),
			ConnectionID: req.ID,
			Name:         req.Name,
			Config:       pluginConnConfig,
		})
	}
	c.JSON(http.StatusCreated, req)
}

func Put(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connNameOrID := c.Param("nameOrID")
	conn, err := pgconnections.New().FetchOneByNameOrID(ctx, connNameOrID)
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

	var req Connection
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if len(req.Command) == 0 {
		switch string(req.Type) {
		case pb.ConnectionTypePostgres:
			req.Command = []string{"psql", "-A", "-F\t", "-P", "pager=off", "-h", "$HOST", "-U", "$USER", "--port=$PORT", "$DB"}
		case pb.ConnectionTypeMySQL:
			req.Command = []string{"mysql", "-h$HOST", "-u$USER", "--port=$PORT", "-D$DB"}
		case pb.ConnectionTypeMSSQL:
			req.Command = []string{
				"sqlcmd", "--exit-on-error", "--trim-spaces", "-r",
				"-S$HOST:$PORT", "-U$USER", "-d$DB", "-i/dev/stdin"}
		}
	}

	req.ID = conn.ID // immutable field
	err = pgconnections.New().Upsert(ctx, pgrest.Connection{
		ID:            conn.ID,
		OrgID:         conn.OrgID,
		AgentID:       req.AgentId,
		LegacyAgentID: req.AgentId,
		Name:          req.Name,
		Command:       req.Command,
		Type:          string(req.Type),
		SubType:       req.SubType,
		Envs:          pgrest.CoerceToMapString(req.Secrets),
	})
	if err != nil {
		log.Errorf("failed updating connection, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
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
		upsertPluginConnection(ctx, pluginName, &types.PluginConnection{
			ID:           uuid.NewString(),
			ConnectionID: conn.ID,
			Name:         req.Name,
			Config:       pluginConnConfig,
		})
	}
	c.JSON(http.StatusOK, req)
}

func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connName := c.Param("name")
	if connName == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "missing connection name"})
		return
	}
	err := pgconnections.New().Delete(ctx, connName)
	switch err {
	case pgrest.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
	case nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		log.Errorf("failed removing connection %v, err=%v", connName, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed removing connection"})
	}
}

func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connList, err := pgconnections.New().FetchAll(ctx)
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
	responseConnList := []Connection{}
	for _, conn := range connList {
		if allowedFn(conn.Name) {
			reviewers, redactTypes := []string{}, []string{}
			for _, pluginConn := range conn.PluginConnection {
				switch pluginConn.Plugin.Name {
				case plugintypes.PluginReviewName:
					reviewers = pluginConn.ConnectionConfig
				case plugintypes.PluginDLPName:
					redactTypes = pluginConn.ConnectionConfig
				}
			}
			responseConnList = append(responseConnList, Connection{
				ID:            conn.ID,
				Name:          conn.Name,
				IconName:      "",
				Command:       conn.Command,
				Type:          conn.Type,
				SubType:       conn.SubType,
				Secrets:       pgrest.CoerceToAnyMap(conn.Envs),
				AgentId:       conn.AgentID,
				Reviewers:     reviewers,
				RedactEnabled: len(redactTypes) > 0,
				RedactTypes:   redactTypes,
			})
		}

	}
	c.JSON(http.StatusOK, responseConnList)
}

func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	conn, err := pgconnections.New().FetchOneByNameOrID(ctx, c.Param("nameOrID"))
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
	reviewers, redactTypes := []string{}, []string{}
	// var redactTypes []string
	for _, pluginConn := range conn.PluginConnection {
		switch pluginConn.Plugin.Name {
		case plugintypes.PluginReviewName:
			reviewers = pluginConn.ConnectionConfig
		case plugintypes.PluginDLPName:
			redactTypes = pluginConn.ConnectionConfig
		}
	}
	c.JSON(http.StatusOK, Connection{
		ID:            conn.ID,
		Name:          conn.Name,
		IconName:      "",
		Command:       conn.Command,
		Type:          conn.Type,
		SubType:       conn.SubType,
		Secrets:       pgrest.CoerceToAnyMap(conn.Envs),
		AgentId:       conn.AgentID,
		Reviewers:     reviewers,
		RedactEnabled: len(redactTypes) > 0,
		RedactTypes:   redactTypes,
	})
}

// DEPRECATED in flavor of POST /api/sessions
func RunExec(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var body sessionapi.SessionPostBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	connName := c.Param("name")
	conn, err := pgconnections.New().FetchOneForExec(ctx, connName)
	if err != nil {
		log.Errorf("failed fetch connection %v for exec, err=%v", connName, err)
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
		c.JSON(http.StatusBadRequest, gin.H{"message": "connection not found"})
		return
	}

	newSession := types.Session{
		ID:           uuid.NewString(),
		OrgID:        ctx.OrgID,
		Labels:       body.Labels,
		Script:       types.SessionScript{"data": body.Script},
		UserEmail:    ctx.UserEmail,
		UserID:       ctx.UserID,
		UserName:     ctx.UserName,
		Type:         conn.Type,
		Connection:   conn.Name,
		Verb:         pb.ClientVerbExec,
		Status:       types.SessionStatusOpen,
		DlpCount:     0,
		StartSession: time.Now().UTC(),
	}
	if err := pgsession.New().Upsert(ctx, newSession); err != nil {
		log.Errorf("failed persisting session, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "The session couldn't be created"})
		return
	}
	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	if userAgent == "webapp.core" {
		userAgent = "webapp.editor.exec"
	}
	sessionapi.RunExec(c, newSession, userAgent, body.ClientArgs)
}

// FetchByName fetches a connection based in access control rules
func FetchByName(ctx pgrest.Context, connectionName string) (*Connection, error) {
	conn, err := pgconnections.New().FetchOneByNameOrID(ctx, connectionName)
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
	// we do not propagate reviewers and redact configuration.
	// it needs to be implemented in the other layers
	return &Connection{
		ID:       conn.ID,
		Name:     conn.Name,
		IconName: "",
		Command:  conn.Command,
		Type:     conn.Type,
		SubType:  conn.SubType,
		Secrets:  pgrest.CoerceToAnyMap(conn.Envs),
		AgentId:  conn.AgentID,
	}, nil
}
