package sessionapi

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/clientexec"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
)

func RunExec(c *gin.Context, session types.Session, clientArgs []string) {
	ctx := user.ContextUser(c)
	log := user.ContextLogger(c)

	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.Org.Id,
		SessionID:      session.ID,
		ConnectionName: session.Connection,
		BearerToken:    getAccessToken(c),
		UserInfo:       nil,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"session_id": nil, "message": err.Error()})
		return
	}
	clientResp := make(chan *clientexec.Response)

	sessionScript := session.Script["data"]

	go func() {
		defer close(clientResp)
		defer client.Close()
		select {
		case clientResp <- client.Run([]byte(sessionScript), nil, clientArgs...):
		default:
		}
	}()
	log = log.With("session", client.SessionID())
	log.Infof("started runexec method for connection %v", session.Connection)
	c.Header("Location", fmt.Sprintf("/api/plugins/audit/sessions/%s/status", client.SessionID()))
	statusCode := http.StatusOK
	select {
	case resp := <-clientResp:
		log.Infof("runexec response. exit_code=%v, truncated=%v, response-length=%v",
			resp.GetExitCode(), resp.Truncated, len(resp.ErrorMessage()))
		if resp.IsError() {
			c.JSON(http.StatusBadRequest, &clientexec.ExecErrResponse{
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
		log.Infof("runexec timeout (50s), it will return async")
		client.Close()
		c.JSON(http.StatusAccepted, gin.H{"session_id": client.SessionID(), "exit_code": nil})
	}
}

func getAccessToken(c *gin.Context) string {
	tokenHeader := c.GetHeader("authorization")
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) > 1 {
		return tokenParts[1]
	}
	return ""
}
