package runbooks

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/apiutils"
	"github.com/runopsio/hoop/common/proto"
	apiconnections "github.com/runopsio/hoop/gateway/api/connections"
	sessionapi "github.com/runopsio/hoop/gateway/api/session"
	"github.com/runopsio/hoop/gateway/clientexec"
	pgsession "github.com/runopsio/hoop/gateway/pgrest/session"
	"github.com/runopsio/hoop/gateway/runbooks/templates"
	"github.com/runopsio/hoop/gateway/storagev2"
	pluginstorage "github.com/runopsio/hoop/gateway/storagev2/plugin"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/user"
)

type Runbook struct {
	Name           string            `json:"name"`
	Metadata       map[string]any    `json:"metadata"`
	ConnectionList []string          `json:"connections,omitempty"`
	EnvVars        map[string]string `json:"-"`
	InputFile      []byte            `json:"-"`
	CommitHash     string            `json:"-"`
}

type RunbookRequest struct {
	FileName   string            `json:"file_name" binding:"required"`
	RefHash    string            `json:"ref_hash"`
	Parameters map[string]string `json:"parameters"`
	ClientArgs []string          `json:"client_args"`
	Redirect   bool              `json:"redirect"`
	Metadata   map[string]any    `json:"metadata"`
}

type RunbookErrResponse struct {
	Message           string  `json:"message"`
	ExitCode          *int    `json:"exit_code"`
	SessionID         *string `json:"session_id"`
	ExecutionTimeMili int64   `json:"execution_time"`
}

type RunbookList struct {
	Items         []*Runbook `json:"items"`
	Commit        string     `json:"commit"`
	CommitAuthor  string     `json:"commit_author"`
	CommitMessage string     `json:"commit_message"`
}

type Service struct {
	PluginService pluginService
}

type Handler struct {
	scanedKnownHosts bool
}

func (h *Handler) ListByConnection(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	log := user.ContextLogger(c)
	connectionName := c.Param("name")
	p, err := pluginstorage.GetByName(ctx, plugintypes.PluginRunbooksName)
	if err != nil {
		log.Error(err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError,
			&RunbookErrResponse{Message: "failed retrieving runbook plugin"})
		return
	}
	if p == nil {
		c.JSON(http.StatusBadRequest, &RunbookErrResponse{Message: "plugin runbooks not found"})
		return
	}
	var configEnvVars map[string]string
	if p.Config != nil {
		configEnvVars = p.Config.EnvVars
	}
	config, err := templates.NewRunbookConfig(configEnvVars)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, &RunbookErrResponse{Message: err.Error()})
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
		c.JSON(http.StatusNotFound, &RunbookErrResponse{Message: "runbooks plugin does not have this connection"})
		return
	}
	runbookList, err := listRunbookFilesByPathPrefix(pathPrefix, config)
	if err != nil {
		log.Infof("failed listing runbooks, err=%v", err)
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"message": fmt.Sprintf("failed listing runbooks, reason=%v", err),
		})
		return
	}
	c.JSON(http.StatusOK, runbookList)
}

func (h *Handler) List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	log := user.ContextLogger(c)
	if !h.scanedKnownHosts {
		knownHostsFilePath, err := templates.SSHKeyScan()
		if err != nil {
			log.Error(err)
			sentry.CaptureException(err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"session_id": nil,
				"message":    "failed scanning known_hosts file, contact"})
			return
		}
		os.Setenv("SSH_KNOWN_HOSTS", knownHostsFilePath)
		h.scanedKnownHosts = true
	}

	p, err := pluginstorage.GetByName(ctx, plugintypes.PluginRunbooksName)
	if err != nil {
		log.Error(err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError,
			&RunbookErrResponse{Message: "failed retrieving runbook plugin"})
		return
	}
	if p == nil {
		c.JSON(http.StatusNotFound, &RunbookErrResponse{Message: "plugin runbooks not found"})
		return
	}
	var configEnvVars map[string]string
	if p.Config != nil {
		configEnvVars = p.Config.EnvVars
	}
	config, err := templates.NewRunbookConfig(configEnvVars)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, &RunbookErrResponse{Message: err.Error()})
		return
	}
	runbookList, err := listRunbookFiles(p.Connections, config)
	if err != nil {
		log.Infof("failed listing runbooks, err=%v", err)
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"message": fmt.Sprintf("failed listing runbooks, reason=%v", err),
		})
		return
	}
	c.PureJSON(http.StatusOK, runbookList)
}

// TODO: Refactor to use sessionapi.RunExec
func (h *Handler) RunExec(c *gin.Context) {
	ctx := user.ContextUser(c)
	log := user.ContextLogger(c)
	storageCtx := storagev2.ParseContext(c)

	var req RunbookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"session_id": nil,
			"message":    err.Error()})
		return
	}
	if err := sessionapi.CoerceMetadataFields(req.Metadata); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"session_id": nil,
			"message":    err.Error()})
		return
	}
	connectionName := c.Param("name")
	config, pathPrefix, err := h.getRunbookConfig(ctx, c, connectionName)
	if err != nil {
		log.Infoln(err)
		return
	}
	if pathPrefix != "" && !strings.HasPrefix(req.FileName, pathPrefix) {
		c.JSON(http.StatusNotFound, gin.H{
			"session_id": nil,
			"message":    fmt.Sprintf("runbook file %v not found", req.FileName)})
		return
	}
	runbook, err := fetchRunbookFile(config, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"session_id": nil,
			"message":    err.Error()})
		return
	}

	runbookParamsJson, _ := json.Marshal(req.Parameters)
	sessionLabels := types.SessionLabels{
		"runbookFile":       req.FileName,
		"runbookParameters": string(runbookParamsJson),
	}

	newSession := types.Session{
		ID:           uuid.NewString(),
		OrgID:        ctx.Org.Id,
		Labels:       sessionLabels,
		Metadata:     req.Metadata,
		Script:       types.SessionScript{"data": string(runbook.InputFile)},
		UserEmail:    ctx.User.Email,
		UserID:       ctx.User.Id,
		Type:         proto.ConnectionTypeCommandLine.String(),
		UserName:     ctx.User.Name,
		Connection:   connectionName,
		Verb:         proto.ClientVerbExec,
		Status:       types.SessionStatusOpen,
		DlpCount:     0,
		StartSession: time.Now().UTC(),
	}

	err = pgsession.New().Upsert(storageCtx, newSession)
	if err != nil {
		log.Errorf("failed persisting session, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "The session couldn't be created"})
		return
	}

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	if userAgent == "webapp.core" {
		userAgent = "webapp.runbook.exec"
	}

	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.Org.Id,
		SessionID:      newSession.ID,
		ConnectionName: connectionName,
		BearerToken:    getAccessToken(c),
		UserAgent:      userAgent,
		UserInfo:       nil,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"session_id": nil, "message": err.Error()})
		return
	}
	clientResp := make(chan *clientexec.Response)
	go func() {
		defer close(clientResp)
		defer client.Close()
		select {
		case clientResp <- client.Run(runbook.InputFile, runbook.EnvVars, req.ClientArgs...):
		default:
		}
	}()

	var params string
	for key, val := range req.Parameters {
		params += fmt.Sprintf("%s:len[%v],", key, len(val))
	}
	log = log.With("session", client.SessionID())
	log.Infof("runbook exec, commit=%s, name=%s, connection=%s, parameters=%v",
		runbook.CommitHash[:8], req.FileName, connectionName, strings.TrimSpace(params))
	c.Header("Location", fmt.Sprintf("/api/plugins/audit/sessions/%s/status", client.SessionID()))
	statusCode := http.StatusOK
	if req.Redirect {
		statusCode = http.StatusFound
	}
	select {
	case resp := <-clientResp:
		log.Infof("runbook exec response. exit_code=%v, truncated=%v, response-length=%v",
			resp.GetExitCode(), resp.Truncated, len(resp.ErrorMessage()))
		if resp.IsError() {
			c.JSON(http.StatusBadRequest, &RunbookErrResponse{
				SessionID:         &resp.SessionID,
				Message:           resp.ErrorMessage(),
				ExitCode:          resp.ExitCode,
				ExecutionTimeMili: resp.ExecutionTimeMili,
			})
			return
		}
		c.JSON(statusCode, resp)
	case <-time.After(time.Second * 50):
		// closing the client will force the goroutine to end
		// and the result will return async
		log.Infof("runbook exec timeout (50s), it will return async")
		client.Close()
		c.JSON(http.StatusAccepted, gin.H{"session_id": client.SessionID(), "exit_code": nil})
	}
}

func (h *Handler) getConnectionID(ctx *user.Context, c *gin.Context, connectionName string) (string, error) {
	conn, err := apiconnections.FetchByName(ctx, connectionName)
	if err != nil {
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, &RunbookErrResponse{Message: "failed retrieving connection"})
		return "", err
	}
	if conn == nil {
		c.JSON(http.StatusNotFound, &RunbookErrResponse{Message: "connection not found"})
		return "", fmt.Errorf("connection not found")
	}
	return conn.ID, nil
}

func (h *Handler) getRunbookConfig(ctx *user.Context, c *gin.Context, connectionName string) (*templates.RunbookConfig, string, error) {
	connectionID, err := h.getConnectionID(ctx, c, connectionName)
	if err != nil {
		return nil, "", err
	}
	ctxv2 := storagev2.NewContext(ctx.User.Id, ctx.Org.Id, storagev2.NewStorage(nil))
	p, err := pluginstorage.GetByName(ctxv2, plugintypes.PluginRunbooksName)
	if err != nil {
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError,
			&RunbookErrResponse{Message: "failed retrieving runbook plugin"})
		return nil, "", fmt.Errorf("failed retrieving runbooks plugin, err=%v", err)
	}
	if p == nil {
		c.JSON(http.StatusNotFound, &RunbookErrResponse{Message: "plugin not found"})
		return nil, "", fmt.Errorf("plugin not found")
	}
	var repoPrefix string
	hasConnection := false
	for _, conn := range p.Connections {
		if conn.ConnectionID == connectionID {
			if len(conn.Config) > 0 {
				repoPrefix = conn.Config[0]
			}
			hasConnection = true
			break
		}
	}
	if !hasConnection {
		c.JSON(http.StatusUnprocessableEntity,
			&RunbookErrResponse{Message: "plugin is not enabled for this connection"})
		return nil, repoPrefix, fmt.Errorf("plugin is not enabled for this connection")
	}
	var configEnvVars map[string]string
	if p.Config != nil {
		configEnvVars = p.Config.EnvVars
	}
	runbookConfig, err := templates.NewRunbookConfig(configEnvVars)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, &RunbookErrResponse{Message: err.Error()})
		return nil, repoPrefix, err
	}
	return runbookConfig, repoPrefix, nil
}
