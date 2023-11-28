package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	"github.com/runopsio/hoop/gateway/clientexec"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/storagev2"
	pluginstorage "github.com/runopsio/hoop/gateway/storagev2/plugin"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Handler struct {
		Service           service
		ConnectionService *connection.Service
		ApiURL            string
	}
	SessionOptionKey string
	SessionOption    struct {
		optionKey SessionOptionKey
		optionVal any
	}

	service interface {
		FindAll(*user.Context, ...*SessionOption) (*SessionList, error)
		FindOne(context *user.Context, name string) (*types.Session, error)
		// EntityHistory(ctx *user.Context, sessionID string) ([]SessionStatusHistory, error)
		// ValidateSessionID(sessionID string) error
		FindReviewBySessionID(ctx *user.Context, sessionID string) (*types.Review, error)
		PersistReview(context *user.Context, review *types.Review) error
	}
)

const (
	OptionUser       SessionOptionKey = "user"
	OptionType       SessionOptionKey = "type"
	OptionConnection SessionOptionKey = "connection"
	OptionStartDate  SessionOptionKey = "start_date"
	OptionEndDate    SessionOptionKey = "end_date"
	OptionOffset     SessionOptionKey = "offset"
	OptionLimit      SessionOptionKey = "limit"
)

const (
	ReviewTypeJit     = "jit"
	ReviewTypeOneTime = "onetime"
)

var (
	availableSessionOptions = []SessionOptionKey{
		OptionUser, OptionType, OptionConnection,
		OptionStartDate, OptionEndDate,
		OptionLimit, OptionOffset,
	}
	downloadTokenStore        = memory.New()
	defaultDownloadExpireTime = time.Minute * 5
)

// func (a *Handler) StatusHistory(c *gin.Context) {
// 	context := user.ContextUser(c)
// 	log := user.ContextLogger(c)

// 	sessionID := c.Param("session_id")
// 	historyList, err := a.Service.EntityHistory(context, sessionID)
// 	if err != nil {
// 		log.Errorf("failed fetching session history, err=%v", err)
// 		sentry.CaptureException(err)
// 		c.AbortWithStatus(http.StatusInternalServerError)
// 		return
// 	}

// 	if historyList == nil {
// 		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
// 		return
// 	}
// 	c.PureJSON(http.StatusOK, historyList)
// }

func (a *Handler) FindOne(c *gin.Context) {
	context := user.ContextUser(c)
	log := user.ContextLogger(c)

	sessionID := c.Param("session_id")
	session, err := a.Service.FindOne(context, sessionID)
	if err != nil {
		log.Errorf("failed fetching session, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching session"})
		return
	}
	if session == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	fileExt := c.Query("extension")
	if fileExt != "" {
		if a.ApiURL == "" {
			c.JSON(http.StatusInternalServerError, gin.H{"message": "failed generating download link, missing api url"})
			return
		}
		hash := sha256.Sum256([]byte(uuid.NewString()))
		downloadToken := hex.EncodeToString(hash[:])
		expireAtTime := time.Now().UTC().Add(defaultDownloadExpireTime).Format(time.RFC3339Nano)
		downloadURL := fmt.Sprintf("%s/api/sessions/%s/download?token=%s&extension=%v&newline=%v&event-time=%v&events=%v",
			a.ApiURL,
			sessionID,
			downloadToken,
			fileExt,
			c.Query("newline"),
			c.Query("event-time"),
			c.Query("events"),
		)
		requestPayload := map[string]any{
			"token":               downloadToken,
			"expire-at":           expireAtTime,
			"context-user-id":     context.User.Id,
			"context-user-groups": context.User.Groups,
			"context-org-id":      context.Org.Id,
		}
		downloadTokenStore.Set(sessionID, requestPayload)
		c.JSON(200, gin.H{"download_url": downloadURL, "expire_at": expireAtTime})
		return
	}

	review, err := a.Service.FindReviewBySessionID(context, sessionID)
	if err != nil {
		return
	}

	if review != nil {
		session.Review = &types.ReviewJSON{
			Id:               review.Id,
			OrgId:            review.OrgId,
			CreatedAt:        review.CreatedAt,
			Type:             review.Type,
			Session:          review.Session,
			Input:            review.Input,
			InputEnvVars:     review.InputEnvVars,
			InputClientArgs:  review.InputClientArgs,
			AccessDuration:   review.AccessDuration,
			Status:           review.Status,
			RevokeAt:         review.RevokeAt,
			ReviewOwner:      review.ReviewOwner,
			Connection:       review.Connection,
			ReviewGroupsData: review.ReviewGroupsData,
		}
	}
	c.PureJSON(http.StatusOK, session)
}

func (a *Handler) DownloadSession(c *gin.Context) {
	sid := c.Param("session_id")
	requestToken := c.Query("token")
	fileExt := c.Query("extension")
	withLineBreak := c.Query("newline") == "1"
	withEventTime := c.Query("event-time") == "1"
	jsonFmt := strings.HasSuffix(fileExt, "json")
	csvFmt := strings.HasSuffix(fileExt, "csv")
	var eventTypes []string
	for _, e := range strings.Split(c.Query("events"), ",") {
		if e == "i" || e == "o" || e == "e" {
			eventTypes = append(eventTypes, e)
		}
	}
	if len(eventTypes) == 0 {
		// default to output, err
		eventTypes = []string{"o", "e"}
	}

	store, _ := downloadTokenStore.Pop(sid).(map[string]any)
	if len(store) == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"status":  http.StatusNotFound,
			"message": "not found"})
		return
	}

	expireAt, err := time.Parse(time.RFC3339Nano, fmt.Sprintf("%v", store["expire-at"]))
	if err != nil {
		log.Errorf("failed parsing request time, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  http.StatusInternalServerError,
			"message": "failed processing request"})
		return
	}
	token := fmt.Sprintf("%v", store["token"])
	if token == "" {
		log.Error("download token is empty")
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  http.StatusInternalServerError,
			"message": "failed processing request"})
		return
	}

	if time.Now().UTC().After(expireAt) {
		c.JSON(http.StatusGone, gin.H{
			"status":  http.StatusGone,
			"message": "session download link expired"})
		return
	}

	userGroups, _ := store["context-user-groups"].([]string)
	usrCtx := &user.Context{
		Org: &user.Org{Id: fmt.Sprintf("%v", store["context-org-id"])},
		User: &user.User{
			Id:     fmt.Sprintf("%v", store["context-user-id"]),
			Groups: userGroups,
		},
	}
	log.With(
		"sid", sid, "ext", fileExt,
		"line-break", withLineBreak, "event-time", withEventTime,
		"jsonfmt", jsonFmt, "csvfmt", csvFmt, "event-types", eventTypes).
		Infof("session download request, valid=%v, org=%v, user=%v, groups=%#v, user-agent=%v",
			token == requestToken, usrCtx.Org.Id, usrCtx.User.Id, userGroups, c.GetHeader("user-agent"))
	if token != requestToken {
		c.JSON(http.StatusUnauthorized, gin.H{
			"status":  http.StatusUnauthorized,
			"message": "unauthorized"})
		return
	}
	session, err := a.Service.FindOne(usrCtx, sid)
	if err != nil || session == nil {
		log.Errorf("failed fetching session, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  http.StatusInternalServerError,
			"message": "failed fetching session"})
		return
	}

	opts := sessionParseOption{withLineBreak, withEventTime, jsonFmt, csvFmt, eventTypes}
	output := parseSessionToFile(session, opts)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s.%s", sid, fileExt))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Accept-Length", fmt.Sprintf("%d", len(output)))
	wrote, err := c.Writer.Write(output)
	log.With("sid", sid).Infof("session downloaded, extension=.%v, wrote=%v, success=%v, err=%v",
		fileExt, wrote, err == nil, err)
}

func (a *Handler) FindAll(c *gin.Context) {
	ctx := user.ContextUser(c)
	log := user.ContextLogger(c)

	var options []*SessionOption
	for _, optKey := range availableSessionOptions {
		if queryOptVal, ok := c.GetQuery(string(optKey)); ok {
			var optVal any
			switch optKey {
			case OptionStartDate, OptionEndDate:
				optTimeVal, err := time.Parse(time.RFC3339, queryOptVal)
				if err != nil {
					log.Warnf("failed listing sessions, wrong start_date option value, err=%v", err)
					c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "failed listing sessions, start_date in wrong format"})
					return
				}
				optVal = optTimeVal
			case OptionLimit, OptionOffset:
				if paginationOptVal, err := strconv.Atoi(queryOptVal); err == nil {
					optVal = paginationOptVal
				}
			case OptionUser:
				// don't let it use this filter if it's not an admin
				if !ctx.User.IsAdmin() {
					continue
				}
				optVal = queryOptVal
			default:
				optVal = queryOptVal
			}
			options = append(options, WithOption(optKey, optVal))
		}
	}
	if !ctx.User.IsAdmin() {
		options = append(options, WithOption(OptionUser, ctx.User.Id))
	}
	sessionList, err := a.Service.FindAll(ctx, options...)
	if err != nil {
		log.Errorf("failed listing sessions, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed listing sessions"})
		return
	}

	c.PureJSON(http.StatusOK, sessionList)
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
func (h *Handler) RunReviewedExec(c *gin.Context) {
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

	review, err := h.Service.FindReviewBySessionID(ctx, sessionId)
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

	session, err := h.Service.FindOne(ctx, sessionId)
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

	p, err := pluginstorage.GetByName(ctxv2, plugintypes.PluginReviewName)
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

	if err != nil {
		log.Errorf("failed persisting session, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "The session couldn't be created"})
		return
	}

	// TODO use the new RunExec here
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
		if err := h.Service.PersistReview(ctx, review); err != nil {
			log.Warnf("failed updating review to executed status, err=%v", err)
		}
		log.Infof("review exec response. exit_code=%v, truncated=%v, response-length=%v",
			resp.GetExitCode(), resp.Truncated, len(resp.ErrorMessage()))

		if resp.IsError() {
			c.JSON(http.StatusBadRequest, &clientexec.ExecErrResponse{
				SessionID: &resp.SessionID,
				Message:   resp.ErrorMessage(),
				ExitCode:  resp.ExitCode,
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
		if err := h.Service.PersistReview(ctx, review); err != nil {
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
