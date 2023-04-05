package runbooks

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	// "github.com/runopsio/hoop/common/log"
	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/clientexec"
	"github.com/runopsio/hoop/gateway/runbooks/templates"
	"github.com/runopsio/hoop/gateway/user"
)

type Runbook struct {
	Name       string            `json:"name"`
	Metadata   map[string]any    `json:"metadata"`
	EnvVars    map[string]string `json:"-"`
	InputFile  []byte            `json:"-"`
	CommitHash string            `json:"-"`
}

type RunbookRequest struct {
	FileName   string            `json:"file_name" binding:"required"`
	RefHash    string            `json:"ref_hash"`
	Parameters map[string]string `json:"parameters"`
	Redirect   bool              `json:"redirect"`
}

type RunbookErrResponse struct {
	Message   string  `json:"message"`
	ExitCode  *int    `json:"exit_code"`
	SessionID *string `json:"session_id"`
}

type RunbookList struct {
	Items         []*Runbook `json:"items"`
	Commit        string     `json:"commit"`
	CommitAuthor  string     `json:"commit_author"`
	CommitMessage string     `json:"commit_message"`
}

type Service struct {
	PluginService     pluginService
	ConnectionService connectionService
}

type Handler struct {
	PluginService     pluginService
	ConnectionService connectionService

	scanedKnownHosts bool
}

func (h *Handler) FindAll(c *gin.Context) {
	ctx := user.ContextUser(c)
	log := user.ContextLogger(c)
	config, err := h.getRunbookConfig(ctx, c, c.Param("name"))
	if err != nil {
		log.Infoln(err)
		return
	}
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
	list, err := listRunbookFiles(config)
	if err != nil {
		log.Infof("failed listing runbooks, err=%v", err)
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"message": fmt.Sprintf("failed listing runbooks, reason=%v", err),
		})
		return
	}
	c.PureJSON(http.StatusOK, list)
}

func (h *Handler) RunExec(c *gin.Context) {
	ctx := user.ContextUser(c)
	log := user.ContextLogger(c)
	var req RunbookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"session_id": nil,
			"message":    err.Error()})
		return
	}
	connectionName := c.Param("name")
	config, err := h.getRunbookConfig(ctx, c, connectionName)
	if err != nil {
		log.Infoln(err)
		return
	}

	if config.PathPrefix != "" && !strings.HasPrefix(req.FileName, config.PathPrefix) {
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

	client, err := clientexec.New(ctx.Org.Id, getAccessToken(c), connectionName, "")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"session_id": nil, "message": err.Error()})
		return
	}
	clientResp := make(chan *clientexec.Response)
	go func() {
		defer close(clientResp)
		defer client.Close()
		select {
		case clientResp <- client.Run(runbook.InputFile, runbook.EnvVars):
		default:
		}
	}()

	var params string
	for key, val := range req.Parameters {
		params += fmt.Sprintf("%s:len[%v],", key, len(val))
	}
	log.With("session", client.SessionID()).Infof("runbook exec, commit=%s, name=%s, connection=%s, parameters=%v",
		runbook.CommitHash[:8], req.FileName, connectionName, strings.TrimSpace(params))
	c.Header("Location", fmt.Sprintf("/api/plugins/audit/sessions/%s/status", client.SessionID()))
	statusCode := http.StatusOK
	if req.Redirect {
		statusCode = http.StatusFound
	}
	select {
	case resp := <-clientResp:
		if resp.IsError() {
			c.JSON(http.StatusBadRequest, &RunbookErrResponse{
				SessionID: &resp.SessionID,
				Message:   resp.ErrorMessage(),
				ExitCode:  resp.ExitCode,
			})
			return
		}
		c.JSON(statusCode, resp)
	case <-time.After(time.Second * 50):
		// closing the client will force the goroutine to end
		// and the result will return async
		client.Close()
		c.JSON(http.StatusAccepted, gin.H{"session_id": client.SessionID(), "exit_code": nil})
	}
}

func (h *Handler) getConnection(ctx *user.Context, c *gin.Context, connectionName string) (string, error) {
	conn, err := h.ConnectionService.FindOne(ctx, connectionName)
	if err != nil {
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, &RunbookErrResponse{Message: "failed retrieving connection"})
		return "", err
	}
	if conn == nil {
		c.JSON(http.StatusNotFound, &RunbookErrResponse{Message: "connection not found"})
		return "", fmt.Errorf("connection not found")
	}
	return conn.Id, nil
}

func (h *Handler) getRunbookConfig(ctx *user.Context, c *gin.Context, connectionName string) (*templates.RunbookConfig, error) {
	connectionID, err := h.getConnection(ctx, c, connectionName)
	if err != nil {
		return nil, err
	}
	p, err := h.PluginService.FindOne(ctx, "runbooks")
	if err != nil {
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError,
			&RunbookErrResponse{Message: "failed retrieving runbook plugin"})
		return nil, fmt.Errorf("failed retrieving runbooks plugin, err=%v", err)
	}
	if p == nil {
		c.JSON(http.StatusNotFound, &RunbookErrResponse{Message: "plugin not found"})
		return nil, fmt.Errorf("plugin not found")
	}
	var repoPrefix string
	hasConnection := false
	for _, conn := range p.Connections {
		if conn.ConnectionId == connectionID {
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
		return nil, fmt.Errorf("plugin is not enabled for this connection")
	}
	var configEnvVars map[string]string
	if p.Config != nil {
		configEnvVars = p.Config.EnvVars
	}
	runbookConfig, err := templates.NewRunbookConfig(repoPrefix, configEnvVars)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, &RunbookErrResponse{Message: err.Error()})
		return nil, err
	}
	return runbookConfig, nil
}
