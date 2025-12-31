package sessionapi

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

// RunReviewedExec
// TODO: Refactor to use sessionapi.RunExec
//
//	@Summary		Reviewed Exec
//	@Description	Run an execution in a reviewed session
//	@Tags			Sessions
//	@Accept			json
//	@Produce		json
//	@Param			session_id		path		string					true	"The id of the resource"
//	@Success		200				{object}	openapi.ExecResponse	"The execution has finished"
//	@Success		202				{object}	openapi.ExecResponse	"The execution is still in progress"
//	@Failure		400,404,409,500	{object}	openapi.HTTPError
//	@Router			/sessions/{session_id}/exec [post]
func RunReviewedExec(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	sessionId := c.Param("session_id")
	apiroutes.SetSidSpanAttr(c, sessionId)
	review, err := models.GetReviewByIdOrSid(ctx.OrgID, sessionId)
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

	if review.Type != models.ReviewTypeOneTime {
		c.JSON(http.StatusNotFound, gin.H{"message": "session not found"})
		return
	}

	session, err := models.GetSessionByID(ctx.OrgID, sessionId)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "session not found"})
		return
	case nil:
	default:
		log.Errorf("failed fetching session, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching sessions"})
		return
	}

	session.BlobInput, err = session.GetBlobInput()
	if err != nil {
		log.Errorf("failed fetching session blob input, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching session input"})
		return
	}

	// The plugin must be active to be able to change the state of the review
	// after the execution, this will ensure that a review is executed only once.
	p, err := models.GetPluginByName(ctx.OrgID, plugintypes.PluginReviewName)
	if err != nil && err != models.ErrNotFound {
		log.Errorf("failed obtaining review plugin, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed retrieving review plugin"})
		return
	}
	hasReviewPlugin := false
	if p != nil {
		for _, conn := range p.Connections {
			if conn.ConnectionName == review.ConnectionName {
				hasReviewPlugin = true
				break
			}
		}
	}

	if hasReviewPlugin {
		err = canExecReviewedSession(ctx, session, review)
		if err != nil {
			log.Infof("cannot execute reviewed session, reason=%v", err)
			c.JSON(http.StatusForbidden, gin.H{"message": err.Error()})
			return
		}
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
		BearerToken:    apiroutes.GetAccessTokenFromRequest(c),
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
		case respCh <- client.Run([]byte(session.BlobInput), review.InputEnvVars, review.InputClientArgs...):
		default:
		}
	}()
	log := log.With("sid", session.ID)
	log.Infof("review apiexec, reviewid=%v, connection=%v, owner=%v, input-lenght=%v",
		review.ID, review.ConnectionName, review.OwnerEmail, len(session.BlobInput))

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*50)
	reviewStatus := models.ReviewStatusExecuted
	defer func() {
		cancelFn()
		if err := models.UpdateReviewStatus(review.OrgID, review.ID, reviewStatus); err != nil {
			log.Warnf("failed updating review to status=%v, err=%v", review.Status, err)
		}
	}()
	select {
	case resp := <-respCh:
		log.Infof("review exec response, %v", resp)
		c.JSON(http.StatusOK, resp)
	case <-timeoutCtx.Done():
		log.Infof("review exec timeout (50s), it will return async")
		// closing the client will force the goroutine to end
		// and the result will return async
		client.Close()

		// we do not know the status of this in the future.
		// replaces the current "PROCESSING" status
		reviewStatus = models.ReviewStatusUnknown
		c.JSON(http.StatusAccepted, clientexec.NewTimeoutResponse(session.ID))
	}
}

func canExecReviewedSession(ctx *storagev2.Context, session *models.Session, review *models.Review) error {
	isAllowed := session.UserEmail == ctx.UserEmail || ctx.IsAuditorOrAdminUser()
	if !isAllowed {
		return fmt.Errorf("unable to execute session")
	}

	if review.Status != models.ReviewStatusApproved {
		return fmt.Errorf("review not approved or already executed")
	}

	if review.TimeWindow != nil {
		switch review.TimeWindow.Type {
		case "time_range":
			startStr, okStart := review.TimeWindow.Configuration["start_time"]
			endStr, okEnd := review.TimeWindow.Configuration["end_time"]
			if !okStart || !okEnd {
				return fmt.Errorf("invalid execution window configuration")
			}

			startTime, err := time.Parse("15:04", startStr)
			if err != nil {
				return fmt.Errorf("invalid execution window start time")
			}
			endTime, err := time.Parse("15:04", endStr)
			if err != nil {
				return fmt.Errorf("invalid execution window end time")
			}

			// Overnight window
			if endTime.Before(startTime) {
				endTime = endTime.Add(24 * time.Hour)
			}

			now := time.Now().UTC()
			nowOnlyTime := time.Date(0, 1, 1, now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.UTC)

			if nowOnlyTime.Before(startTime) || nowOnlyTime.After(endTime) {
				return fmt.Errorf("execution not allowed outside the time window %s to %s UTC", startStr, endStr)
			}
		default:
			return fmt.Errorf("unknown execution window type %s", review.TimeWindow.Type)
		}
	}

	return nil
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
