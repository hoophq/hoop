package sessionapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/guardrails"
	"github.com/hoophq/hoop/gateway/jira"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	transportsystem "github.com/hoophq/hoop/gateway/transport/system"
)

var (
	downloadTokenStore         = memory.New()
	defaultDownloadExpireTime  = time.Minute * 5
	internalExitCode           = 254
	defaultMaxSessionListLimit = 100
)

type SessionPostBody struct {
	Script     string              `json:"script"`
	Connection string              `json:"connection"`
	Labels     types.SessionLabels `json:"labels"`
	Metadata   map[string]any      `json:"metadata"`
	ClientArgs []string            `json:"client_args"`
	JiraFields map[string]string   `json:"jira_fields"`
}

// RunExec
//
//	@Summary				Exec
//	@Description.markdown	run-exec
//	@Tags					Sessions
//	@Accept					json
//	@Produce				json
//	@Param					request		body		openapi.ExecRequest		true	"The request body resource"
//	@Success				200			{object}	openapi.ExecResponse	"The execution has finished"
//	@Success				202			{object}	openapi.ExecResponse	"The execution is still in progress"
//	@Failure				400,422,500	{object}	openapi.HTTPError
//	@Router					/sessions [post]
func Post(c *gin.Context) {
	sid := uuid.NewString()
	apiroutes.SetSidSpanAttr(c, sid)

	ctx := storagev2.ParseContext(c)
	var req SessionPostBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	// Accept request body and url params as connection name
	// Maintained for compatibility with legacy endpoint /api/connections/:name/exec
	if req.Connection == "" {
		req.Connection = c.Param("name")
	}
	if err := CoerceMetadataFields(req.Metadata); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	conn, err := apiconnections.FetchByName(ctx, req.Connection)
	if err != nil {
		log.Errorf("failed fetch connection %v for exec, err=%v", req.Connection, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if conn == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("connection %v not found", req.Connection)})
		return
	}

	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	if userAgent == "webapp.core" {
		userAgent = "webapp.editor.exec"
	}
	log := log.With("sid", sid, "user", ctx.UserEmail)
	newSession := models.Session{
		ID:                   sid,
		OrgID:                ctx.OrgID,
		Labels:               req.Labels,
		Metadata:             req.Metadata,
		IntegrationsMetadata: nil,
		Metrics:              nil,
		BlobInput:            models.BlobInputType(req.Script),
		UserEmail:            ctx.UserEmail,
		UserID:               ctx.UserID,
		UserName:             ctx.UserName,
		ConnectionType:       conn.Type,
		ConnectionSubtype:    conn.SubType.String,
		Connection:           conn.Name,
		Verb:                 pb.ClientVerbExec,
		Status:               string(openapi.SessionStatusOpen),
		CreatedAt:            time.Now().UTC(),
		EndSession:           nil,
	}

	connRules, err := models.GetConnectionGuardRailRules(ctx.OrgID, conn.Name)
	if err != nil {
		log.Errorf("failed obtaining guard rail rules from connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed obtaining guard rail rules"})
		return
	}

	if connRules != nil {
		err = guardrails.Validate("input", connRules.GuardRailInputRules, []byte(req.Script))
		switch err.(type) {
		case *guardrails.ErrRuleMatch:
			// persist session to audit this attempt
			_ = models.UpsertSession(newSession)
			encErr := base64.StdEncoding.EncodeToString([]byte(err.Error()))
			if err := models.UpdateSessionEventStream(models.SessionDone{
				ID:         sid,
				OrgID:      ctx.OrgID,
				EndSession: func() *time.Time { t := time.Now().UTC(); return &t }(),
				BlobStream: fmt.Appendf(nil, `[[0, "e", %q]]`, encErr),
				ExitCode:   func() *int { v := internalExitCode; return &v }(),
				Status:     string(openapi.SessionStatusDone),
			}); err != nil {
				log.Errorf("unable to update session, err=%v", err)
			}
			c.JSON(http.StatusOK, clientexec.Response{
				SessionID:         sid,
				Output:            err.Error(),
				OutputStatus:      "failed",
				ExitCode:          internalExitCode,
				ExecutionTimeMili: 0,
			})
			return
		case nil:
		default:
			errMsg := fmt.Sprintf("internal error, failed validating guard rails input rules: %v", err)
			log.Error(errMsg)
			c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
			return
		}
	}

	if conn.JiraIssueTemplateID.String != "" {
		issueTemplate, jiraConfig, err := models.GetJiraIssueTemplatesByID(conn.OrgID, conn.JiraIssueTemplateID.String)
		if err != nil {
			log.Errorf("failed obtaining jira issue template for %v, reason=%v", conn.Name, err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed obtaining jira issue template: %v", err)})
			return
		}
		if jiraConfig != nil && jiraConfig.IsActive() {
			if req.JiraFields == nil {
				req.JiraFields = map[string]string{}
			}
			jiraFields, err := jira.ParseIssueFields(issueTemplate, req.JiraFields, newSession)
			switch err.(type) {
			case *jira.ErrInvalidIssueFields:
				c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
				return
			case nil:
			default:
				log.Error(err)
				c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
				return
			}
			resp, err := jira.CreateCustomerRequest(issueTemplate, jiraConfig, jiraFields)
			if err != nil {
				log.Error(err)
				c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
				return
			}
			newSession.IntegrationsMetadata = map[string]any{
				"jira_issue_key": resp.IssueKey,
				"jira_issue_url": resp.Links.Agent,
			}
		}
	}

	if err := models.UpsertSession(newSession); err != nil {
		log.Errorf("failed creating session, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed creating session"})
		return
	}

	// TODO: refactor to use response from openapi package
	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.GetOrgID(),
		SessionID:      sid,
		ConnectionName: conn.Name,
		BearerToken:    getAccessToken(c),
		UserAgent:      userAgent,
	})
	if err != nil {
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	log.Infof("started runexec method for connection %v", conn.Name)
	respCh := make(chan *clientexec.Response)
	go func() {
		defer func() { close(respCh); client.Close() }()
		select {
		case respCh <- client.Run([]byte(req.Script), nil, req.ClientArgs...):
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
		c.JSON(http.StatusAccepted, clientexec.NewTimeoutResponse(sid))
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

// ListSessions
//
//	@Summary		List Sessions
//	@Description	List session resources
//	@Tags			Sessions
//	@Produce		json
//	@Param			user			query		string	false	"Filter by user's subject id"
//	@Param			connection		query		string	false	"Filter by connection's name"
//	@Param			type			query		string	false	"Filter by connection's type"
//	@Param			review.approver	query		string	false	"Filter by the approver's email of a review"
//	@Param			review.status	query		string	false	"Filter by the review status"
//	@Param			start_date		query		string	false	"Filter starting on this date"	Format(RFC3339)
//	@Param			end_date		query		string	false	"Filter ending on this date"	Format(RFC3339)
//	@Param			limit			query		int		false	"Limit the amount of records to return (max: 100)"
//	@Param			offset			query		int		false	"Offset to paginate through resources"
//	@Success		200				{object}	openapi.SessionList
//	@Failure		500				{object}	openapi.HTTPError
//	@Router			/sessions [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	option := models.NewSessionOption()
	for _, optKey := range openapi.AvailableSessionOptions {
		if queryOptVal, ok := c.GetQuery(string(optKey)); ok {
			switch optKey {
			case openapi.SessionOptionUser:
				if !ctx.IsAuditorOrAdminUser() {
					continue
				}
				option.User = queryOptVal
			case openapi.SessionOptionConnection:
				option.ConnectionName = queryOptVal
			case openapi.SessionOptionType:
				option.ConnectionType = queryOptVal
			case openapi.SessionOptionReviewStatus:
				option.ReviewStatus = queryOptVal
			case openapi.SessionOptionReviewApproverEmail:
				option.ReviewApproverEmail = &queryOptVal
			case openapi.SessionOptionStartDate:
				optTimeVal, err := time.Parse(time.RFC3339, queryOptVal)
				if err != nil {
					log.Warnf("failed listing sessions, wrong start_date option value, err=%v", err)
					c.JSON(http.StatusUnprocessableEntity, gin.H{
						"message": "failed listing sessions, start_date or end_date in wrong format"})
					return
				}
				option.StartDate = sql.NullString{
					String: optTimeVal.Format(time.RFC3339),
					Valid:  true,
				}
			case openapi.SessionOptionEndDate:
				optTimeVal, err := time.Parse(time.RFC3339, queryOptVal)
				if err != nil {
					log.Warnf("failed listing sessions, wrong end_date option value, err=%v", err)
					c.JSON(http.StatusUnprocessableEntity, gin.H{
						"message": "failed listing sessions, start_date or end_date in wrong format"})
					return
				}
				option.EndDate = sql.NullString{
					String: optTimeVal.Format(time.RFC3339),
					Valid:  true,
				}
			case openapi.SessionOptionLimit:
				option.Limit, _ = strconv.Atoi(queryOptVal)
			case openapi.SessionOptionOffset:
				option.Offset, _ = strconv.Atoi(queryOptVal)
			}
		}
	}

	if option.Limit > defaultMaxSessionListLimit {
		option.Limit = defaultMaxSessionListLimit
	}

	if option.StartDate.Valid && !option.EndDate.Valid {
		option.EndDate = sql.NullString{
			String: time.Now().UTC().Format(time.RFC3339),
			Valid:  true,
		}
	}

	sessionList, err := models.ListSessions(ctx.OrgID, option)
	if err != nil {
		log.Errorf("failed listing sessions (v2), err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed listing sessions (v2)"})
		return
	}

	c.PureJSON(http.StatusOK, toOpenApiSessionList(sessionList))
}

// GetSessionByID
//
//	@Summary				Get Session
//	@Description.markdown	get-session-by-id
//	@Tags					Sessions
//	@Param					extension	query	openapi.SessionGetByIDParams	false	"-"
//	@Param					session_id	path	string							true	"The id of the resource"
//	@Produce				json
//	@Success				200		{object}	openapi.Session
//	@Failure				404,500	{object}	openapi.HTTPError
//	@Router					/sessions/{session_id} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	sessionID := c.Param("session_id")
	apiroutes.SetSidSpanAttr(c, sessionID)
	session, err := models.GetSessionByID(ctx.OrgID, sessionID)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	case nil:
	default:
		log.Errorf("failed fetching session, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching session"})
		return
	}

	// display or allow download the session stream only for the owner, admin or auditor roles
	isAllowed := session.UserID == ctx.UserID || ctx.IsAuditorOrAdminUser()
	fileExt := c.Query("extension")
	if fileExt != "" {
		if appconfig.Get().DisableSessionsDownload() || !isAllowed {
			c.JSON(http.StatusForbidden, gin.H{
				"status":  http.StatusForbidden,
				"message": "user is not allowed to download this session"})
			return
		}

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

	if isAllowed {
		blobStream, err := session.GetBlobStream()
		if err != nil {
			log.Errorf("failed fetching blob stream from session, err=%v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching blob stream from session"})
			return
		}
		session.BlobStream = blobStream.BlobStream
	}

	if option := c.Query("event_stream"); option != "" {
		output, err := parseBlobStream(session, sessionParseOption{events: []string{"o", "e"}})
		if err != nil {
			log.With("sid", sessionID).Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": "failed parsing blob stream"})
			return
		}
		switch option {
		case "utf8":
			session.BlobStream = json.RawMessage(fmt.Sprintf(`[%q]`, string(output)))
			session.BlobStreamSize = int64(int64(utf8.RuneCountInString(string(output))))
		case "base64":
			encOutput := base64.StdEncoding.EncodeToString(output)
			session.BlobStream = json.RawMessage(fmt.Sprintf(`[%q]`, encOutput))
			session.BlobStreamSize = int64(len(encOutput))
		}
	}

	obj := toOpenApiSession(session)
	expandedFieldParts := strings.Split(c.Query("expand"), ",")
	if !slices.Contains(expandedFieldParts, "event_stream") {
		obj.EventStream = nil
	}
	// encode the object manually to obtain any encoding errors.
	c.Writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(c.Writer).Encode(obj); err != nil {
		log.With("sid", sessionID).Errorf("failed encoding session, removing event stream, reason=%v", err)
		obj.EventStream = []byte(`["An internal error occurred: unable to decode the event stream due to invalid characters.\nPlease download the session as an alternative."]`)
		_ = json.NewEncoder(c.Writer).Encode(obj)
	}
	c.Writer.WriteHeader(http.StatusOK)
}

// DownloadSession
//
//	@Summary		Download Session
//	@Description	Download session by id
//	@Tags			Sessions
//	@Produce		octet-stream,json
//	@Param			session_id	path		string	true	"The id of the resource"
//	@Success		200			{string}	string
//	@Header			200			{string}	Content-Type		"application/octet-stream"
//	@Header			200			{string}	Content-Disposition	"application/octet-stream"
//	@Header			200			{integer}	Accept-Length		"size in bytes of the content"
//	@Failure		404,500		{object}	openapi.HTTPError
//	@Router			/sessions/{session_id}/download [get]
func DownloadSession(c *gin.Context) {
	sid := c.Param("session_id")
	apiroutes.SetSidSpanAttr(c, sid)
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
	session, err := models.GetSessionByID(ctx.OrgID, sid)
	if err != nil || session == nil {
		log.Errorf("failed fetching session, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  http.StatusInternalServerError,
			"message": "failed fetching session"})
		return
	}
	blob, err := session.GetBlobStream()
	if err != nil {
		log.Errorf("failed fetching blob stream from session, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching blob stream from session"})
		return
	}
	session.BlobStream = blob.BlobStream
	output, err := parseBlobStream(session, sessionParseOption{
		withLineBreak: withLineBreak,
		withEventTime: withEventTime,
		withJsonFmt:   jsonFmt,
		withCsvFmt:    csvFmt,
		events:        eventTypes,
	})
	if err != nil {
		log.Errorf("failed parsing blob stream, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  http.StatusInternalServerError,
			"message": "failed parsing blob stream"})
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s.%s", sid, fileExt))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Accept-Length", fmt.Sprintf("%d", len(output)))
	wrote, err := c.Writer.Write(output)
	log.With("sid", sid).Infof("session downloaded, extension=.%v, output-size=%v, wrote=%v, success=%v, err=%v",
		fileExt, len(output), wrote, err == nil, err)
}

// PatchMetadata
//
//	@Summary	Update Session Metadata
//	@Tags		Sessions
//	@Accept		json
//	@Produce	json
//	@Param		request	body	openapi.SessionUpdateMetadataRequest	true	"The request body resource"
//	@Success	204
//	@Failure	400,404,500	{object}	openapi.HTTPError
//	@Router		/sessions/{session_id}/metadata [patch]
func PatchMetadata(c *gin.Context) {
	ctx, sessionID := storagev2.ParseContext(c), c.Param("session_id")
	apiroutes.SetSidSpanAttr(c, sessionID)
	var req openapi.SessionUpdateMetadataRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	err := models.UpdateSessionMetadata(ctx.OrgID, ctx.UserEmail, sessionID, req.Metadata)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	case nil:
	default:
		msgErr := fmt.Sprintf("failed to update session metadata, reason=%v", err)
		log.Error(msgErr)
		c.JSON(http.StatusInternalServerError, gin.H{"message": msgErr})
		return
	}
	c.Writer.WriteHeader(http.StatusNoContent)
}

// KillSession
//
//	@Summary	Kill Session
//	@Tags		Sessions
//	@Param		session_id	path	string	true	"The id of the resource"
//	@Success	204
//	@Failure	400,404,500	{object}	openapi.HTTPError
//	@Router		/sessions/{session_id}/kill [post]
func Kill(c *gin.Context) {
	ctx, sid := storagev2.ParseContext(c), c.Param("session_id")
	apiroutes.SetSidSpanAttr(c, sid)

	sess, err := models.GetSessionByID(ctx.OrgID, sid)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "session not found"})
		return
	case nil:
		// if user is not admin and session is not owned by user, return 404
		if sess.UserID != ctx.UserID && !ctx.IsAdmin() {
			c.JSON(http.StatusNotFound, gin.H{"message": "session not found"})
			return
		}
	default:
		log.Errorf("failed fetching session, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching session"})
		return
	}

	log.With("user", ctx.UserEmail, "sid", sid).Infof("user initiated a kill process")
	if err := transportsystem.KillSession(sid); err != nil {
		log.With("sid", sid).Warnf("failed killing session, reason=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusNoContent, nil)
}
