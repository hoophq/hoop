package apirunbooks

import (
	"context"
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
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/api/runbooks/templates"
	sessionapi "github.com/hoophq/hoop/gateway/api/session"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgplugins "github.com/hoophq/hoop/gateway/pgrest/plugins"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

// ListRunbooks
//
//	@Summary		List Runbooks
//	@Description	List all Runbooks
//	@Tags			Core
//	@Produce		json
//	@Success		200			{object}	openapi.RunbookList
//	@Failure		404,422,500	{object}	openapi.HTTPError
//	@Router			/plugins/runbooks/templates [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	p, err := pgplugins.New().FetchOne(ctx, plugintypes.PluginRunbooksName)
	if err != nil {
		log.Errorf("failed retrieving runbook plugin, reason=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError,
			gin.H{"message": "failed retrieving runbook plugin"})
		return
	}
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "plugin runbooks not found"})
		return
	}
	var configEnvVars map[string]string
	if p.Config != nil {
		configEnvVars = p.Config.EnvVars
	}
	config, err := templates.NewRunbookConfig(configEnvVars)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	runbookList, err := listRunbookFiles(p.Connections, config)
	if err != nil {
		log.Infof("failed listing runbooks, err=%v", err)
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("failed listing runbooks, reason=%v", err)})
		return
	}
	c.PureJSON(http.StatusOK, runbookList)
}

// ListRunbooksByConnection
//
//	@Summary		List Runbooks By Connection
//	@Description	List Runbooks templates by connection
//	@Tags			Core
//	@Produce		json
//	@Param			name			path		string	true	"The name of the connection"
//	@Success		200				{object}	openapi.RunbookList
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/plugins/runbooks/connections/{name}/templates [get]
func ListByConnection(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	connectionName := c.Param("name")
	p, err := pgplugins.New().FetchOne(ctx, plugintypes.PluginRunbooksName)
	if err != nil {
		log.Errorf("failed retrieving runbook plugin, reason=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed retrieving runbook plugin"})
		return
	}
	if p == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "plugin runbooks not found"})
		return
	}
	var configEnvVars map[string]string
	if p.Config != nil {
		configEnvVars = p.Config.EnvVars
	}
	config, err := templates.NewRunbookConfig(configEnvVars)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	hasConnection := false
	var pathPrefix string
	for _, conn := range p.Connections {
		if conn.Name == connectionName {
			if len(conn.Config) > 0 {
				pathPrefix = conn.Config[0]
			}
			hasConnection = true
			break
		}
	}
	if !hasConnection {
		c.JSON(http.StatusNotFound, gin.H{"message": "runbooks plugin does not have this connection"})
		return
	}
	runbookList, err := listRunbookFilesByPathPrefix(pathPrefix, config)
	if err != nil {
		log.Infof("failed listing runbooks, err=%v", err)
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("failed listing runbooks, reason=%v", err)})
		return
	}
	c.JSON(http.StatusOK, runbookList)
}

// RunRunbookExec
//
//	@Summary		Runbook Exec
//	@Description	Start a execution using a Runbook as input
//	@Tags			Core
//	@Accept			json
//	@Produce		json
//	@Param			name			path		string					true	"The name of the connection"
//	@Param			request			body		openapi.RunbookRequest	true	"The request body resource"
//	@Success		200				{object}	openapi.ExecResponse	"The execution has finished"
//	@Success		202				{object}	openapi.ExecResponse	"The execution is still in progress"
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/plugins/runbooks/connections/{name}/exec [post]
func RunExec(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.RunbookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if err := sessionapi.CoerceMetadataFields(req.Metadata); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	connectionName := c.Param("name")
	connection, err := getConnection(ctx, c, connectionName)
	if err != nil {
		log.Error(err)
		return
	}
	config, pathPrefix, err := getRunbookConfig(ctx, c, connection)
	if err != nil {
		log.Error(err)
		return
	}
	if pathPrefix != "" && !strings.HasPrefix(req.FileName, pathPrefix) {
		c.JSON(http.StatusNotFound, gin.H{"message": fmt.Sprintf("runbook file %v not found", req.FileName)})
		return
	}
	runbook, err := fetchRunbookFile(config, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	for key, val := range req.EnvVars {
		// don't replace environment variables from runbook
		if _, ok := runbook.EnvVars[key]; ok {
			continue
		}
		runbook.EnvVars[key] = val
	}

	runbookParamsJson, _ := json.Marshal(req.Parameters)
	sessionLabels := types.SessionLabels{
		"runbookFile":       req.FileName,
		"runbookParameters": string(runbookParamsJson),
	}

	sessionID := uuid.NewString()
	apiroutes.SetSidSpanAttr(c, sessionID)
	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	if userAgent == "webapp.core" {
		userAgent = "webapp.runbook.exec"
	}

	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.GetOrgID(),
		SessionID:      sessionID,
		ConnectionName: connectionName,
		BearerToken:    getAccessToken(c),
		UserAgent:      userAgent,
		Origin:         proto.ConnectionOriginClientAPIRunbooks,
	})
	if err != nil {
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	newSession := models.Session{
		ID:                   sessionID,
		OrgID:                ctx.GetOrgID(),
		Connection:           connectionName,
		ConnectionType:       string(proto.ConnectionTypeCustom),
		ConnectionSubtype:    connection.SubType.String,
		Verb:                 proto.ClientVerbExec,
		Labels:               sessionLabels,
		Metadata:             req.Metadata,
		IntegrationsMetadata: nil,
		Metrics:              nil,
		BlobInput:            models.BlobInputType(runbook.InputFile),
		UserID:               ctx.UserID,
		UserName:             ctx.UserName,
		UserEmail:            ctx.UserEmail,
		Status:               string(openapi.SessionStatusOpen),
		ExitCode:             nil,
		CreatedAt:            time.Now().UTC(),
		EndSession:           nil,
	}

	if err := models.UpsertSession(newSession); err != nil {
		log.Errorf("failed persisting session, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "The session couldn't be created"})
		return
	}

	var params string
	for key, val := range req.Parameters {
		params += fmt.Sprintf("%s:len[%v],", key, len(val))
	}
	log := log.With("sid", sessionID)
	log.Infof("runbook exec, commit=%s, name=%s, connection=%s, parameters=%v",
		runbook.CommitHash[:8], req.FileName, connectionName, strings.TrimSpace(params))

	respCh := make(chan *clientexec.Response)
	go func() {
		defer func() { close(respCh); client.Close() }()
		select {
		case respCh <- client.Run(runbook.InputFile, runbook.EnvVars, req.ClientArgs...):
		default:
		}
	}()

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*50)
	defer cancelFn()
	select {
	case outcome := <-respCh:
		log.Infof("runbook exec response, %v", outcome)
		c.JSON(http.StatusOK, outcome)
	case <-timeoutCtx.Done():
		client.Close()
		log.Infof("runbook exec timeout (50s), it will return async")
		c.JSON(http.StatusAccepted, clientexec.NewTimeoutResponse(sessionID))
	}
}

func getConnection(ctx pgrest.Context, c *gin.Context, connectionName string) (*models.Connection, error) {
	conn, err := apiconnections.FetchByName(ctx, connectionName)
	if err != nil {
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed retrieving connection"})
		return nil, err
	}
	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
		return nil, fmt.Errorf("connection not found")
	}
	return conn, nil
}

func getRunbookConfig(ctx pgrest.Context, c *gin.Context, connection *models.Connection) (*templates.RunbookConfig, string, error) {
	p, err := pgplugins.New().FetchOne(ctx, plugintypes.PluginRunbooksName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed retrieving runbook plugin"})
		return nil, "", fmt.Errorf("failed retrieving runbooks plugin, err=%v", err)
	}
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "plugin not found"})
		return nil, "", fmt.Errorf("plugin not found")
	}
	var repoPrefix string
	hasConnection := false
	for _, conn := range p.Connections {
		if conn.ConnectionID == connection.ID {
			if len(conn.Config) > 0 {
				repoPrefix = conn.Config[0]
			}
			hasConnection = true
			break
		}
	}
	if !hasConnection {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "plugin is not enabled for this connection"})
		return nil, repoPrefix, fmt.Errorf("plugin is not enabled for this connection")
	}
	var configEnvVars map[string]string
	if p.Config != nil {
		configEnvVars = p.Config.EnvVars
	}
	runbookConfig, err := templates.NewRunbookConfig(configEnvVars)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return nil, repoPrefix, err
	}
	return runbookConfig, repoPrefix, nil
}
