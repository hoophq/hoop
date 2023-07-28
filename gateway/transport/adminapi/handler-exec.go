package adminapi

import (
	"encoding/base64"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/clientexec"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type ExecRequest struct {
	UserInfo      types.APIContext  `json:"user_info"  binding:"required"`
	Connection    string            `json:"connection" binding:"required"`
	SessionID     string            `json:"session_id"`
	Input         string            `json:"input"`
	InputEncoding string            `json:"input_encoding"`
	ClientEnvVars map[string]string `json:"client_envvars"`
	ClientArgs    []string          `json:"client_args"`
}

type ExecResponse struct {
	SessionID *string `json:"session_id"`
	ExitCode  *int    `json:"exit_code"`
	Message   string  `json:"message"`
}

func execPost(c *gin.Context) {
	var req ExecRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.PureJSON(http.StatusBadRequest, gin.H{"message": &ExecResponse{Message: err.Error()}})
		return
	}
	if req.InputEncoding == "base64" {
		input, err := base64.StdEncoding.DecodeString(req.Input)
		if err != nil {
			c.PureJSON(http.StatusBadRequest, &ExecResponse{Message: "failed decoding (base64) input"})
			return
		}
		req.Input = string(input)
	}

	if err := validateExecRequest(req, c); err != nil {
		return
	}
	authKey, cancelFn := authRequest()
	// remove the authentication key from memory
	defer cancelFn()

	client, err := clientexec.New(&clientexec.Options{
		OrgID:          req.UserInfo.OrgID,
		SessionID:      req.SessionID,
		ConnectionName: req.Connection,
		BearerToken:    authKey,
		UserInfo:       &req.UserInfo,
	})
	if err != nil {
		c.PureJSON(http.StatusBadRequest, &ExecResponse{Message: err.Error()})
		return
	}
	clientResp := make(chan *clientexec.Response)
	go func() {
		defer close(clientResp)
		defer client.Close()
		select {
		case clientResp <- client.Run([]byte(req.Input), req.ClientEnvVars, req.ClientArgs...):
		default:
		}
	}()

	log := log.With("session", client.SessionID())
	log.Infof("admin exec, connection=%s, user=%v, input=%v, envvars=%v, args=%v",
		req.Connection, req.UserInfo.UserEmail, len(req.Input), len(req.ClientEnvVars), req.ClientArgs)

	select {
	case resp := <-clientResp:
		log.Infof("admin exec response. exit_code=%v, truncated=%v, response-length=%v",
			resp.GetExitCode(), resp.Truncated, len(resp.ErrorMessage()))
		if resp.IsError() {
			c.PureJSON(http.StatusBadRequest, &ExecResponse{
				SessionID: &resp.SessionID,
				Message:   resp.ErrorMessage(),
				ExitCode:  resp.ExitCode,
			})
			return
		}
		c.PureJSON(http.StatusOK, resp)
	case <-time.After(time.Second * 50):
		// closing the client will force the goroutine to end
		// and the result will return async
		log.Infof("admin exec timeout (50s), it will return async")
		client.Close()
		c.PureJSON(http.StatusAccepted, gin.H{"session_id": client.SessionID(), "exit_code": nil})
	}
}

func validateExecRequest(req ExecRequest, c *gin.Context) error {
	u := req.UserInfo
	if u.OrgID == "" || u.UserID == "" || u.UserEmail == "" {
		c.PureJSON(http.StatusBadRequest,
			&ExecResponse{Message: "missing required user_info request attributes"},
		)
		return io.EOF
	}
	return nil
}
