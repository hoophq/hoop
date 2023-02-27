package runbooks

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/grpc"
	pb "github.com/runopsio/hoop/common/proto"

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
}

func (h *Handler) FindAll(c *gin.Context) {
	obj, _ := c.Get("context")
	ctx, _ := obj.(*user.Context)
	config, err := h.getRunbookConfig(ctx, c, c.Param("name"))
	if err != nil {
		log.Println(err)
		return
	}
	list, err := listRunbookFiles(config)
	if err != nil {
		log.Printf("failed listing runbooks, err=%v", err)
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"message": fmt.Sprintf("failed listing runbooks, reason=%v", err),
		})
		return
	}
	c.PureJSON(http.StatusOK, list)
}

func (h *Handler) RunExec(c *gin.Context) {
	obj, _ := c.Get("context")
	ctx, _ := obj.(*user.Context)
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
		log.Println(err)
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

	sessionID := uuid.NewString()
	client, err := grpc.Connect("127.0.0.1:8010", getAccessToken(c),
		grpc.WithOption(grpc.OptionConnectionName, connectionName),
		grpc.WithOption("origin", pb.ConnectionOriginClientAPI),
		grpc.WithOption("verb", pb.ClientVerbExec),
		grpc.WithOption("session_id", sessionID))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"session_id": nil, "message": err.Error()})
		return
	}
	clientResp := make(chan clientExecResponse)
	go func() {
		defer close(clientResp)
		defer client.Close()
		var encEnvVars []byte
		if len(runbook.EnvVars) > 0 {
			encEnvVars, err = pb.GobEncode(runbook.EnvVars)
			if err != nil {
				clientResp <- clientExecResponse{err, nilExitCode}
				return
			}
		}
		exitCode, err := processClientExec(runbook.InputFile, encEnvVars, client)
		select {
		case clientResp <- clientExecResponse{err, exitCode}:
		default:
		}
	}()

	var params string
	for key, val := range req.Parameters {
		params += fmt.Sprintf("%s:len[%v],", key, len(val))
	}
	log.Printf("session=%s - runbook exec, commit=%s, name=%s, connection=%s, parameters=%v",
		sessionID, runbook.CommitHash[:8], req.FileName, connectionName, strings.TrimSpace(params))
	c.Header("Location", fmt.Sprintf("/api/plugins/audit/sessions/%s/status", sessionID))
	statusCode := http.StatusOK
	if req.Redirect {
		statusCode = http.StatusFound
	}
	select {
	case resp := <-clientResp:
		switch resp.exitCode {
		case nilExitCode:
			// means the gRPC client returned an error in the client flow
			c.JSON(statusCode, &RunbookErrResponse{
				SessionID: &sessionID,
				Message:   fmt.Sprintf("%v", resp.err)})
		case 0:
			c.JSON(statusCode, gin.H{"session_id": sessionID, "exit_code": 0})
		default:
			c.JSON(http.StatusBadRequest, &RunbookErrResponse{
				SessionID: &sessionID,
				Message:   fmt.Sprintf("%v", resp.err),
				ExitCode:  &resp.exitCode})
		}
	case <-time.After(time.Second * 50):
		// closing the client will force the goroutine to end
		// and the result will return async
		client.Close()
		c.JSON(statusCode, gin.H{"session_id": sessionID, "exit_code": nil})
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
