package sessionapi

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/apiutils"
	"github.com/runopsio/hoop/gateway/clientexec"
	pgplugins "github.com/runopsio/hoop/gateway/pgrest/plugins"
	pgreview "github.com/runopsio/hoop/gateway/pgrest/review"
	"github.com/runopsio/hoop/gateway/storagev2"
	sessionstorage "github.com/runopsio/hoop/gateway/storagev2/session"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/user"
)

func RunExec(c *gin.Context, session types.Session, userAgent string, clientArgs []string) {
	ctx := user.ContextUser(c)
	log := user.ContextLogger(c)

	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.Org.Id,
		SessionID:      session.ID,
		ConnectionName: session.Connection,
		BearerToken:    getAccessToken(c),
		UserAgent:      userAgent,
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

// TODO: Refactor to use sessionapi.RunExec
func RunReviewedExec(c *gin.Context) {
	ctxv2 := storagev2.ParseContext(c)
	ctx := user.ContextUser(c)
	log := user.ContextLogger(c)

	sessionId := c.Param("session_id")

	var req clientexec.ExecRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"session_id": sessionId,
			"message":    err.Error()})
		return
	}

	review, err := pgreview.New().FetchOneBySid(ctx, sessionId)
	if err != nil {
		log.Errorf("failed retrieving review, err=%v", err)
		c.JSON(http.StatusInternalServerError, &clientexec.ExecErrResponse{Message: "failed retrieving review"})
		return
	}

	if review == nil {
		c.JSON(http.StatusNotFound, &clientexec.ExecErrResponse{Message: "reviewed session not found"})
		return
	}

	// TODO review this, maybe we don't need anymore
	// reviewID := review.Id
	if isLockedForExec(sessionId) {
		errMsg := fmt.Sprintf("the session %v is already being processed", sessionId)
		c.JSON(http.StatusConflict, &clientexec.ExecErrResponse{Message: errMsg})
		return
	}

	// locking the execution per review id prevents race condition executions
	// in case of misbehavior of clients
	lockExec(sessionId)
	defer unlockExec(sessionId)

	if review == nil || review.Type != ReviewTypeOneTime {
		c.JSON(http.StatusNotFound, &clientexec.ExecErrResponse{Message: "session not found"})
		return
	}

	session, err := sessionstorage.FindOne(ctxv2, sessionId)
	if err != nil {
		log.Errorf("failed fetching session, reason=%v", err)
		c.JSON(http.StatusInternalServerError, &clientexec.ExecErrResponse{Message: "failed fetching sessions"})
		return
	}
	if session == nil {
		c.JSON(http.StatusNotFound, &clientexec.ExecErrResponse{Message: "session not found"})
		return
	}
	if session.UserEmail != ctx.User.Email {
		c.JSON(http.StatusBadRequest, &clientexec.ExecErrResponse{Message: "only the creator can trigger this action"})
		return
	}
	if review.Status != types.ReviewStatusApproved {
		c.JSON(http.StatusBadRequest, &clientexec.ExecErrResponse{Message: "review not approved or already executed"})
		return
	}

	p, err := pgplugins.New().FetchOne(ctx, plugintypes.PluginReviewName)
	if err != nil {
		log.Errorf("failed obtaining review plugin, err=%v", err)
		c.JSON(http.StatusInternalServerError, &clientexec.ExecErrResponse{Message: "failed retrieving review plugin"})
		return
	}
	hasReviewPlugin := false
	if p != nil {
		for _, conn := range p.Connections {
			if conn.Name == review.Connection.Name {
				hasReviewPlugin = true
				break
			}
		}
	}

	// The plugin must be active to be able to change the state of the review
	// after the execution, this will ensure that a review is executed only once.
	if !hasReviewPlugin {
		errMsg := fmt.Sprintf("review plugin is not enabled for the connection %s", review.Connection.Name)
		log.Infof(errMsg)
		c.JSON(http.StatusUnprocessableEntity, &clientexec.ExecErrResponse{Message: errMsg})
		return
	}

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	if userAgent == "webapp.core" {
		userAgent = "webapp.review.exec"
	}

	// TODO use the new RunExec here
	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.Org.Id,
		SessionID:      session.ID,
		ConnectionName: session.Connection,
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
		case clientResp <- client.Run([]byte(session.Script["data"]), review.InputEnvVars, review.InputClientArgs...):
		default:
		}
	}()
	log = log.With("session", client.SessionID())
	log.Infof("review apiexec, reviewid=%v, connection=%v, owner=%v, input-lenght=%v",
		review.Id, review.Connection.Name, review.CreatedBy, len(review.Input))
	c.Header("Location", fmt.Sprintf("/api/plugins/audit/sessions/%s/status", client.SessionID()))

	select {
	case resp := <-clientResp:
		review.Status = types.ReviewStatusExecuted
		if _, err := sessionstorage.PutReview(ctxv2, review); err != nil {
			log.Warnf("failed updating review to executed status, err=%v", err)
		}
		log.Infof("review exec response. exit_code=%v, truncated=%v, response-length=%v",
			resp.GetExitCode(), resp.Truncated, len(resp.ErrorMessage()))

		if resp.IsError() {
			c.JSON(http.StatusBadRequest, &clientexec.ExecErrResponse{
				SessionID:         &resp.SessionID,
				Message:           resp.ErrorMessage(),
				ExitCode:          resp.ExitCode,
				ExecutionTimeMili: resp.ExecutionTimeMili,
			})
			return
		}
		c.JSON(http.StatusOK, resp)
	case <-time.After(time.Second * 50):
		log.Infof("review exec timeout (50s), it will return async")
		// closing the client will force the goroutine to end
		// and the result will return async
		client.Close()

		// we do not know the status of this in the future.
		// replaces the current "PROCESSING" status
		review.Status = types.ReviewStatusUnknown
		if _, err := sessionstorage.PutReview(ctxv2, review); err != nil {
			log.Warnf("failed updating review to unknown status, err=%v", err)
		}

		c.JSON(http.StatusAccepted, gin.H{"session_id": client.SessionID(), "exit_code": nil})
	}
}

var syncMutexExecMap = sync.RWMutex{}
var mutexExecMap = map[string]any{}

func lockExec(reviewID string) {
	syncMutexExecMap.Lock()
	defer syncMutexExecMap.Unlock()
	mutexExecMap[reviewID] = nil
}

func unlockExec(reviewID string) {
	syncMutexExecMap.Lock()
	defer syncMutexExecMap.Unlock()
	delete(mutexExecMap, reviewID)
}

func isLockedForExec(reviewID string) bool {
	syncMutexExecMap.Lock()
	defer syncMutexExecMap.Unlock()
	_, ok := mutexExecMap[reviewID]
	return ok
}
