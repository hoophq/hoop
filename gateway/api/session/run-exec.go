package sessionapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/gateway/clientexec"
	pgplugins "github.com/hoophq/hoop/gateway/pgrest/plugins"
	pgreview "github.com/hoophq/hoop/gateway/pgrest/review"
	pgusers "github.com/hoophq/hoop/gateway/pgrest/users"
	"github.com/hoophq/hoop/gateway/storagev2"
	sessionstorage "github.com/hoophq/hoop/gateway/storagev2/session"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

func getAccessToken(c *gin.Context) string {
	tokenHeader := c.GetHeader("authorization")
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) > 1 {
		return tokenParts[1]
	}
	return ""
}

// RunReviewedExec
// TODO: Refactor to use sessionapi.RunExec
//
//	@Summary		Reviewed Exec
//	@Description	Run an execution in a reviewed session
//	@Tags			Core
//	@Accept			json
//	@Produce		json
//	@Param			session_id		path		string					true	"The id of the resource"
//	@Success		200				{object}	openapi.ExecResponse	"The execution has finished"
//	@Success		202				{object}	openapi.ExecResponse	"The execution is still in progress"
//	@Failure		400,404,409,500	{object}	openapi.HTTPError
//	@Router			/sessions/{session_id}/exec [post]
func RunReviewedExec(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	log := pgusers.ContextLogger(c)

	sessionId := c.Param("session_id")
	review, err := pgreview.New().FetchOneBySid(ctx, sessionId)
	if err != nil {
		log.Errorf("failed retrieving review, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed retrieving review"})
		return
	}

	if review == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "reviewed session not found"})
		return
	}

	// TODO review this, maybe we don't need anymore
	// reviewID := review.Id
	if isLockedForExec(sessionId) {
		errMsg := fmt.Sprintf("the session %v is already being processed", sessionId)
		c.JSON(http.StatusConflict, gin.H{"message": errMsg})
		return
	}

	// locking the execution per review id prevents race condition executions
	// in case of misbehavior of clients
	lockExec(sessionId)
	defer unlockExec(sessionId)

	if review.Type != ReviewTypeOneTime {
		c.JSON(http.StatusNotFound, gin.H{"message": "session not found"})
		return
	}

	session, err := sessionstorage.FindOne(ctx, sessionId)
	if err != nil {
		log.Errorf("failed fetching session, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching sessions"})
		return
	}
	if session == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "session not found"})
		return
	}
	if session.UserEmail != ctx.UserEmail {
		c.JSON(http.StatusBadRequest, gin.H{"message": "only the creator can trigger this action"})
		return
	}
	if review.Status != types.ReviewStatusApproved {
		c.JSON(http.StatusBadRequest, gin.H{"message": "review not approved or already executed"})
		return
	}

	p, err := pgplugins.New().FetchOne(ctx, plugintypes.PluginReviewName)
	if err != nil {
		log.Errorf("failed obtaining review plugin, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed retrieving review plugin"})
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
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": errMsg})
		return
	}

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	if userAgent == "webapp.core" {
		userAgent = "webapp.review.exec"
	}

	// TODO: refactor to use response from openapi package
	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.GetOrgID(),
		SessionID:      session.ID,
		ConnectionName: session.Connection,
		BearerToken:    getAccessToken(c),
		UserAgent:      userAgent,
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
		case respCh <- client.Run([]byte(session.Script["data"]), review.InputEnvVars, review.InputClientArgs...):
		default:
		}
	}()
	log = log.With("sid", session.ID)
	log.Infof("review apiexec, reviewid=%v, connection=%v, owner=%v, input-lenght=%v",
		review.Id, review.Connection.Name, review.CreatedBy, len(review.Input))

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*50)
	defer cancelFn()
	select {
	case resp := <-respCh:
		review.Status = types.ReviewStatusExecuted
		if _, err := sessionstorage.PutReview(ctx, review); err != nil {
			log.Warnf("failed updating review to executed status, err=%v", err)
		}
		log.Infof("review exec response, %v", resp)
		c.JSON(http.StatusOK, resp)
	case <-timeoutCtx.Done():
		log.Infof("review exec timeout (50s), it will return async")
		// closing the client will force the goroutine to end
		// and the result will return async
		client.Close()

		// we do not know the status of this in the future.
		// replaces the current "PROCESSING" status
		review.Status = types.ReviewStatusUnknown
		if _, err := sessionstorage.PutReview(ctx, review); err != nil {
			log.Warnf("failed updating review to unknown status, err=%v", err)
		}
		c.JSON(http.StatusAccepted, clientexec.NewTimeoutResponse(session.ID))
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
