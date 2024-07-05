package sessionapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	"github.com/hoophq/hoop/gateway/clientexec"
	pgreview "github.com/hoophq/hoop/gateway/pgrest/review"
	pgsession "github.com/hoophq/hoop/gateway/pgrest/session"
	pgusers "github.com/hoophq/hoop/gateway/pgrest/users"
	"github.com/hoophq/hoop/gateway/storagev2"
	sessionstorage "github.com/hoophq/hoop/gateway/storagev2/session"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

type SessionPostBody struct {
	Script     string              `json:"script"`
	Connection string              `json:"connection"`
	Labels     types.SessionLabels `json:"labels"`
	Metadata   map[string]any      `json:"metadata"`
	ClientArgs []string            `json:"client_args"`
}

func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	log := pgusers.ContextLogger(c)
	var body SessionPostBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	// Accept request body and url params as connection name
	// Maintained for compatibility with legacy endpoint /api/connections/:name/exec
	if body.Connection == "" {
		body.Connection = c.Param("name")
	}
	if err := CoerceMetadataFields(body.Metadata); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	conn, err := apiconnections.FetchByName(ctx, body.Connection)
	if err != nil {
		log.Errorf("failed fetch connection %v for exec, err=%v", body.Connection, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if conn == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("connection %v not found", body.Connection)})
		return
	}

	sessionID := uuid.NewString()
	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	if userAgent == "webapp.core" {
		userAgent = "webapp.editor.exec"
	}

	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.GetOrgID(),
		SessionID:      sessionID,
		ConnectionName: conn.Name,
		BearerToken:    getAccessToken(c),
		UserAgent:      userAgent,
	})
	if err != nil {
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	newSession := types.Session{
		ID:           sessionID,
		OrgID:        ctx.OrgID,
		Labels:       body.Labels,
		Metadata:     body.Metadata,
		Script:       types.SessionScript{"data": body.Script},
		UserEmail:    ctx.UserEmail,
		UserID:       ctx.UserID,
		UserName:     ctx.UserName,
		Type:         conn.Type,
		Connection:   conn.Name,
		Verb:         pb.ClientVerbExec,
		Status:       types.SessionStatusOpen,
		StartSession: time.Now().UTC(),
	}
	if err := pgsession.New().Upsert(ctx, newSession); err != nil {
		log.Errorf("failed creating session, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed creating session"})
		return
	}

	log = log.With("sid", sessionID)
	log.Infof("started runexec method for connection %v", conn.Name)
	respCh := make(chan *clientexec.Response)
	go func() {
		defer func() { close(respCh); client.Close() }()
		select {
		case respCh <- client.Run([]byte(body.Script), nil, body.ClientArgs...):
		default:
		}
	}()
	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*50)
	defer cancelFn()
	select {
	case outcome := <-respCh:
		log.Infof("runexec response, %v", outcome)
		c.JSON(http.StatusOK, outcome)
	case <-timeoutCtx.Done():
		client.Close()
		log.Infof("runexec timeout (50s), it will return async")
		c.JSON(http.StatusAccepted, clientexec.NewTimeoutResponse(sessionID))
	}
}

func CoerceMetadataFields(metadata map[string]any) error {
	if len(metadata) > 20 {
		return fmt.Errorf("metadata field must have less than 10 fields")
	}
	for key, val := range metadata {
		val := fmt.Sprintf("%v", val)
		if len(key) >= 2500 || len(val) >= 2500 {
			return fmt.Errorf("metadata key or value must not contain more than 2500 characteres")
		}
		// convert to string
		metadata[key] = val
	}
	return nil
}

func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	log := pgusers.ContextLogger(c)

	var options []*types.SessionOption
	for _, optKey := range types.AvailableSessionOptions {
		if queryOptVal, ok := c.GetQuery(string(optKey)); ok {
			var optVal any
			switch optKey {
			case types.SessionOptionStartDate, types.SessionOptionEndDate:
				optTimeVal, err := time.Parse(time.RFC3339, queryOptVal)
				if err != nil {
					log.Warnf("failed listing sessions, wrong start_date option value, err=%v", err)
					c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "failed listing sessions, start_date in wrong format"})
					return
				}
				optVal = optTimeVal
			case types.SessionOptionLimit, types.SessionOptionOffset:
				if paginationOptVal, err := strconv.Atoi(queryOptVal); err == nil {
					optVal = paginationOptVal
				}
			case types.SessionOptionUser:
				// don't let it use this filter if it's not an admin
				if !ctx.IsAdminUser() {
					continue
				}
				optVal = queryOptVal
			default:
				optVal = queryOptVal
			}
			options = append(options, WithOption(optKey, optVal))
		}
	}
	if !ctx.IsAdminUser() {
		options = append(options, WithOption(types.SessionOptionUser, ctx.UserID))
	}
	sessionList, err := sessionstorage.List(ctx, options...)
	if err != nil {
		log.Errorf("failed listing sessions, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed listing sessions"})
		return
	}

	c.PureJSON(http.StatusOK, sessionList)
}

func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	log := pgusers.ContextLogger(c)

	sessionID := c.Param("session_id")
	session, err := sessionstorage.FindOne(ctx, sessionID)
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
		if ctx.ApiURL == "" {
			c.JSON(http.StatusInternalServerError, gin.H{"message": "failed generating download link, missing api url"})
			return
		}
		hash := sha256.Sum256([]byte(uuid.NewString()))
		downloadToken := hex.EncodeToString(hash[:])
		expireAtTime := time.Now().UTC().Add(defaultDownloadExpireTime).Format(time.RFC3339Nano)
		downloadURL := fmt.Sprintf("%s/api/sessions/%s/download?token=%s&extension=%v&newline=%v&event-time=%v&events=%v",
			ctx.ApiURL,
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
			"context-user-id":     ctx.UserID,
			"context-user-groups": ctx.UserGroups,
			"context-org-id":      ctx.GetOrgID(),
		}
		downloadTokenStore.Set(sessionID, requestPayload)
		c.JSON(200, gin.H{"download_url": downloadURL, "expire_at": expireAtTime})
		return
	}

	review, err := pgreview.New().FetchOneBySid(ctx, sessionID)
	if err != nil {
		log.Errorf("failed fetching review, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed obtaining review"})
		return
	}

	// TODO: refactor to use the postgrest direct function
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

	if c.Query("event_stream") == "utf8" {
		output := parseSessionToFile(session, sessionParseOption{events: []string{"o", "e"}})
		session.EventStream = []any{string(output)}
	}
	c.PureJSON(http.StatusOK, map[string]any{
		"id":           session.ID,
		"org_id":       session.OrgID,
		"script":       session.Script,
		"labels":       session.Labels,
		"metadata":     session.Metadata,
		"metrics":      session.Metrics,
		"user":         session.UserEmail,
		"user_id":      session.UserID,
		"user_name":    session.UserName,
		"type":         session.Type,
		"connection":   session.Connection,
		"review":       session.Review,
		"verb":         session.Verb,
		"status":       session.Status,
		"event_stream": session.EventStream,
		"event_size":   session.EventSize,
		"start_date":   session.StartSession,
		"end_date":     session.EndSession,
	})
}

func DownloadSession(c *gin.Context) {
	// ctx := storagev2.ParseContext(c)
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

	ctx := storagev2.NewContext(
		fmt.Sprintf("%v", store["context-user-id"]),
		fmt.Sprintf("%v", store["context-org-id"]))
	ctx.UserGroups, _ = store["context-user-groups"].([]string)
	log.With(
		"sid", sid, "ext", fileExt,
		"line-break", withLineBreak, "event-time", withEventTime,
		"jsonfmt", jsonFmt, "csvfmt", csvFmt, "event-types", eventTypes).
		Infof("session download request, valid=%v, org=%v, user=%v, groups=%#v, user-agent=%v",
			token == requestToken, ctx.OrgID, ctx.UserID, ctx.UserGroups, apiutils.NormalizeUserAgent(c.Request.Header.Values))
	if token != requestToken {
		c.JSON(http.StatusUnauthorized, gin.H{
			"status":  http.StatusUnauthorized,
			"message": "unauthorized"})
		return
	}
	session, err := sessionstorage.FindOne(ctx, sid)
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
