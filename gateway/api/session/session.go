package sessionapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/analytics"
	apiai "github.com/hoophq/hoop/gateway/api/ai"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/jira"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	transportsystem "github.com/hoophq/hoop/gateway/transport/system"
	"github.com/hoophq/hoop/gateway/utils"
	"gorm.io/gorm"
)

var (
	downloadTokenStore         = memory.New()
	defaultDownloadExpireTime  = time.Minute * 5
	internalExitCode           = 254
	defaultMaxSessionListLimit = 100
)

type SessionPostBody struct {
	Script         string                    `json:"script"`
	Connection     string                    `json:"connection"`
	EnvVars        map[string]string         `json:"env_vars"`
	Labels         openapi.SessionLabelsType `json:"labels"`
	Metadata       map[string]any            `json:"metadata"`
	ClientArgs     []string                  `json:"client_args"`
	JiraFields     map[string]string         `json:"jira_fields"`
	SessionBatchID *string                   `json:"session_batch_id"`
	CorrelationID  *string                   `json:"correlation_id"`
}

func AIAnalyze(ctx context.Context, orgID uuid.UUID, connName, script string) (*models.SessionAIAnalysis, error) {
	aiAnalyzerRule, err := models.GetAISessionAnalyzerRuleByConnection(models.DB, orgID, connName)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed obtaining ai session analyzer rule for connection %v, reason: %w", connName, err)
	}

	if aiAnalyzerRule != nil {
		analyzerRes, err := apiai.AnalyzeSession(ctx, orgID, script)
		if err != nil {
			return nil, fmt.Errorf("failed analyzing session, reason: %w", err)
		}

		var action models.RiskEvaluationAction
		switch analyzerRes.RiskLevel {
		case apiai.RiskLevelHigh:
			action = aiAnalyzerRule.RiskEvaluation.HighRiskAction
		case apiai.RiskLevelMedium:
			action = aiAnalyzerRule.RiskEvaluation.MediumRiskAction
		case apiai.RiskLevelLow:
			action = aiAnalyzerRule.RiskEvaluation.LowRiskAction
		}

		return &models.SessionAIAnalysis{
			RiskLevel:   string(analyzerRes.RiskLevel),
			Title:       analyzerRes.Title,
			Explanation: analyzerRes.Explanation,
			Action:      string(action),
		}, nil
	}

	return nil, nil
}

func canAccessSession(ctx *storagev2.Context, session *models.Session) bool {
	if session.UserID == ctx.UserID || ctx.IsAuditorOrAdminUser() {
		return true
	}

	if session.Review != nil {
		reviewersGroups := make([]string, 0)
		for _, group := range session.Review.ReviewGroups {
			reviewersGroups = append(reviewersGroups, group.GroupName)
		}

		return utils.SlicesHasIntersection(ctx.UserGroups, reviewersGroups)
	}

	return false
}

// Post Sessions
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
	trackClient := analytics.New()
	defer trackClient.Close()

	ctx := storagev2.ParseContext(c)
	var req SessionPostBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if err := CoerceMetadataFields(req.Metadata); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	if err := ValidateCorrelationID(req.CorrelationID); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	conn, err := models.GetConnectionByNameOrID(ctx, req.Connection)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetch connection %v for exec, err=%v", req.Connection, err)
		return
	}
	if conn == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("connection %v not found", req.Connection)})
		return
	}

	for key := range req.EnvVars {
		if _, ok := conn.Envs[key]; ok {
			delete(req.EnvVars, key)
		}
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
		ConnectionTags:       conn.ConnectionTags,
		Verb:                 proto.ClientVerbExec,
		Status:               string(openapi.SessionStatusOpen),
		IdentityType:         "user",
		SessionBatchID:       req.SessionBatchID,
		CorrelationID:        req.CorrelationID,
		CreatedAt:            time.Now().UTC(),
		EndSession:           nil,
	}

	orgID := uuid.MustParse(ctx.GetOrgID())
	analyzeRes, err := AIAnalyze(c, orgID, conn.Name, req.Script)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed analyzing session")
		return
	}

	if analyzeRes != nil {
		newSession.AIAnalysis = analyzeRes

		shouldBlock := analyzeRes.Action == string(models.BlockExecution)
		if shouldBlock {
			newSession.Status = string(openapi.SessionStatusDone)
			newSession.ExitCode = &internalExitCode
			endTime := time.Now().UTC()
			newSession.EndSession = &endTime
		}

		if err := models.UpsertSession(newSession); err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating session")
			return
		}

		if shouldBlock {
			trackClient.TrackSessionUsageData(analytics.EventSessionFinished, ctx.OrgID, ctx.UserID, sid)

			c.JSON(http.StatusOK, clientexec.Response{
				SessionID:         sid,
				Output:            "Session blocked by AI risk analyzer",
				OutputStatus:      "blocked",
				ExitCode:          internalExitCode,
				ExecutionTimeMili: 0,
				AIAnalysis:        toOpenApiSessionAIAnalysis(analyzeRes),
			})
			return
		}
	}

	if conn.JiraIssueTemplateID.String != "" {
		issueTemplate, jiraConfig, err := models.GetJiraIssueTemplatesByID(conn.OrgID, conn.JiraIssueTemplateID.String)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed obtaining jira issue template for %v: %v", conn.Name, err)
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
				httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed parsing jira issue fields: %v", err)
				return
			}
			resp, err := jira.CreateCustomerRequest(issueTemplate, jiraConfig, jiraFields)
			if err != nil {
				httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating jira customer request: %v", err)
				return
			}
			newSession.IntegrationsMetadata = map[string]any{
				"jira_issue_key": resp.IssueKey,
				"jira_issue_url": resp.Links.Agent,
			}
		}
	}

	if err := services.ValidateAndUpsertSession(c, newSession, conn); err != nil {
		log.Errorf("failed creating session, err=%v", err)

		if errors.Is(err, services.ErrMissingMetadata) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
			return
		}

		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating session")
		return
	}
	trackClient.TrackSessionUsageData(analytics.EventSessionCreated, ctx.OrgID, ctx.UserID, sid)

	// TODO: refactor to use response from openapi package
	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.GetOrgID(),
		SessionID:      sid,
		ConnectionName: conn.Name,
		BearerToken:    apiroutes.GetAccessTokenFromRequest(c),
		UserAgent:      userAgent,
	})
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating client: %v", err)
		return
	}

	log.Infof("started runexec method for connection %v", conn.Name)
	respCh := make(chan *clientexec.Response)
	go func() {
		defer func() { close(respCh); client.Close() }()
		select {
		case respCh <- client.Run([]byte(req.Script), req.EnvVars, req.ClientArgs...):
		default:
		}
	}()
	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*50)
	defer cancelFn()
	select {
	case outcome := <-respCh:
		log.Infof("runexec response, %v", outcome)
		outcome.AIAnalysis = toOpenApiSessionAIAnalysis(analyzeRes)
		c.JSON(http.StatusOK, outcome)
	case <-timeoutCtx.Done():
		client.Close()
		log.Infof("runexec timeout (50s), it will return async")
		c.JSON(http.StatusAccepted, clientexec.NewTimeoutResponse(sid))
	}
}

// ValidateCorrelationID ensures the correlation id is bounded and printable.
// Accepts nil/empty (treated as absent). Shared by REST, provision, runbook and gRPC paths.
func ValidateCorrelationID(v *string) error {
	if v == nil || *v == "" {
		return nil
	}
	s := *v
	if len(s) > 255 {
		return fmt.Errorf("correlation_id must not exceed 255 characters")
	}
	for _, r := range s {
		if r < 0x20 || r > 0x7E {
			return fmt.Errorf("correlation_id must contain only printable ASCII characters")
		}
	}
	return nil
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
//	@Param			correlation_id	query		string	false	"Filter by external workflow/task correlation id"
//	@Param			jira_issue_key	query		string	false	"Filter by Jira issue key"
//	@Param			start_date		query		string	false	"Filter starting on this date"	Format(RFC3339)
//	@Param			end_date		query		string	false	"Filter ending on this date"	Format(RFC3339)
//	@Param			limit			query		int		false	"Limit the amount of records to return (max: 100)"
//	@Param			offset			query		int		false	"Offset to paginate through resources"
//	@Success		200				{object}	openapi.SessionList
//	@Failure		500				{object}	openapi.HTTPError
//	@Router			/sessions [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	// Lazy cleanup of expired credential sessions
	err := models.CloseExpiredCredentialSessions()
	if err != nil {
		log.Errorf("failed to close expired credential sessions, err=%v", err)
	}

	option := models.NewSessionOption()
	for _, optKey := range openapi.AvailableSessionOptions {
		if queryOptVal, ok := c.GetQuery(string(optKey)); ok {
			switch optKey {
			case openapi.SessionOptionUser:
				option.User = queryOptVal
			case openapi.SessionOptionConnection:
				option.ConnectionName = queryOptVal
			case openapi.SessionOptionType:
				option.ConnectionType = queryOptVal
			case openapi.SessionOptionReviewStatus:
				option.ReviewStatus = queryOptVal
			case openapi.SessionOptionReviewApproverEmail:
				option.ReviewApproverEmail = &queryOptVal
			case openapi.SessionOptionBatchID:
				option.BatchID = &queryOptVal
			case openapi.SessionOptionCorrelationID:
				if queryOptVal != "" {
					option.CorrelationID = &queryOptVal
				}
			case openapi.SessionOptionJiraIssueKey:
				keys := strings.Split(queryOptVal, ",")
				for i, k := range keys {
					keys[i] = strings.ToLower(strings.TrimSpace(k))
				}
				option.JiraIssueKey = keys
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

	sessionList, err := models.ListSessions(ctx.OrgID, ctx.UserID, ctx.IsAuditorOrAdminUser(), option)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing sessions (v2)")
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
//	@Success				200				{object}	openapi.Session
//	@Failure				403,404,422,500	{object}	openapi.HTTPError
//	@Router					/sessions/{session_id} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	// Lazy cleanup of expired credential sessions
	err := models.CloseExpiredCredentialSessions()
	if err != nil {
		log.Errorf("failed to close expired credential sessions, err=%v", err)
	}

	sessionID := c.Param("session_id")
	apiroutes.SetSidSpanAttr(c, sessionID)
	session, err := models.GetSessionByID(ctx.OrgID, sessionID)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	case nil:
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching session")
		return
	}

	canAccessSession := canAccessSession(ctx, session)
	if !canAccessSession {
		c.JSON(http.StatusForbidden, gin.H{"message": "user is not allowed to access this session"})
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
			httputils.AbortWithErr(c, http.StatusInternalServerError, errors.New("missing api url"), "failed generating download link, missing api url")
			return
		}
		hash := sha256.Sum256([]byte(uuid.NewString()))
		downloadToken := hex.EncodeToString(hash[:])
		expireAtTime := time.Now().UTC().Add(defaultDownloadExpireTime).Format(time.RFC3339Nano)
		blobType := c.Query("blob-type")

		var downloadURL string
		if blobType == "session_input" {
			downloadURL = fmt.Sprintf("%s/api/sessions/%s/download/input?token=%s",
				ctx.ApiURL,
				sessionID,
				downloadToken,
			)
		} else {
			downloadURL = fmt.Sprintf("%s/api/sessions/%s/download?token=%s&extension=%v&newline=%v&event-time=%v&events=%v",
				ctx.ApiURL,
				sessionID,
				downloadToken,
				fileExt,
				c.Query("newline"),
				c.Query("event-time"),
				c.Query("events"),
			)
		}
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

	// it will only load the blob stream if it's allowed and the client requested to expand the attribute
	expandParam := c.Query("expand")
	expandedFields := strings.Split(expandParam, ",")

	expandEventStream := slices.Contains(expandedFields, "event_stream")
	if isAllowed && expandEventStream {
		session.BlobStream, err = session.GetBlobStream()
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching blob stream from session")
			return
		}
	}

	// Load input by default for backward compat (no expand param),
	// or when explicitly requested via ?expand=session_input
	if expandParam == "" || slices.Contains(expandedFields, "session_input") {
		session.BlobInput, err = session.GetBlobInput()
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching input from session")
			return
		}
	}

	mustParseBlobStream := c.Query("event_stream") != "" && expandEventStream
	if mustParseBlobStream {
		err = encodeBlobStream(session, openapi.SessionEventStreamType(c.Query("event_stream")))
		switch err {
		case errEventStreamUnsupportedFormat:
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": errEventStreamUnsupportedFormat.Error()})
			return
		case nil:
		default:
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed parsing blob stream")
			return
		}
	}
	obj := toOpenApiSession(session, true) // always include script in response (old behavior)

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
//	@Param			session_id		path		string	true	"The id of the resource"
//	@Success		200				{string}	string
//	@Header			200				{string}	Content-Type		"application/octet-stream"
//	@Header			200				{string}	Content-Disposition	"application/octet-stream"
//	@Header			200				{integer}	Accept-Length		"size in bytes of the content"
//	@Failure		401,404,410,500	{object}	openapi.HTTPError
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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed processing request")
		return
	}
	token := fmt.Sprintf("%v", store["token"])
	if token == "" {
		log.Error("download token is empty")
		httputils.AbortWithErr(c, http.StatusInternalServerError, errors.New("download token is empty"), "failed processing request")
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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching session")
		return
	}
	session.BlobStream, err = session.GetBlobStream()
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching blob stream from session")
		return
	}
	output, err := parseBlobStream(session, sessionParseOption{
		withLineBreak: withLineBreak,
		withEventTime: withEventTime,
		withJsonFmt:   jsonFmt,
		withCsvFmt:    csvFmt,
		events:        eventTypes,
	})
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed parsing blob stream")
		return
	}

	now := time.Now().UTC()
	timePart := now.Format("20060102-150405")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s-%s-%s.%s", session.Connection, sid, timePart, fileExt))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Accept-Length", fmt.Sprintf("%d", len(output)))
	wrote, err := c.Writer.Write(output)
	log.With("sid", sid).Infof("session downloaded, extension=.%v, output-size=%v, wrote=%v, success=%v, err=%v",
		fileExt, len(output), wrote, err == nil, err)
}

// DownloadSessionInput
//
//	@Summary		Download Session Input Command
//	@Description	Download session input session by id
//	@Tags			Sessions
//	@Produce		octet-stream,json
//	@Param			session_id		path		string	true	"The id of the resource"
//	@Success		200				{string}	string
//	@Header			200				{string}	Content-Type		"application/octet-stream"
//	@Header			200				{string}	Content-Disposition	"application/octet-stream"
//	@Header			200				{integer}	Accept-Length		"size in bytes of the content"
//	@Failure		401,404,410,500	{object}	openapi.HTTPError
//	@Router			/sessions/{session_id}/download/input [get]
func DownloadSessionInput(c *gin.Context) {
	sid := c.Param("session_id")
	requestToken := c.Query("token")

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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed processing request")
		return
	}
	token := fmt.Sprintf("%v", store["token"])
	if token == "" {
		log.Error("download token is empty")
		httputils.AbortWithErr(c, http.StatusInternalServerError, errors.New("download token is empty"), "failed processing request")
		return
	}

	if time.Now().UTC().After(expireAt) {
		c.JSON(http.StatusGone, gin.H{
			"status":  http.StatusGone,
			"message": "session download link expired"})
		return
	}

	fileExt := "txt"
	ctx := storagev2.NewContext(
		fmt.Sprintf("%v", store["context-user-id"]),
		fmt.Sprintf("%v", store["context-org-id"]))
	ctx.UserGroups, _ = store["context-user-groups"].([]string)
	log.With("sid", sid, "ext", fileExt).
		Infof("session download input request, valid=%v, org=%v, user=%v, groups=%#v, user-agent=%v",
			token == requestToken, ctx.OrgID, ctx.UserID, ctx.UserGroups, apiutils.NormalizeUserAgent(c.Request.Header.Values))
	if token != requestToken {
		c.JSON(http.StatusUnauthorized, gin.H{
			"status":  http.StatusUnauthorized,
			"message": "unauthorized"})
		return
	}
	session, err := models.GetSessionByID(ctx.OrgID, sid)
	if err != nil || session == nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching session")
		return
	}
	output, err := session.GetBlobInput()
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching input from session: %v", err)
		return
	}

	now := time.Now().UTC()
	timePart := now.Format("20060102-150405")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s-%s-%s.%s", session.Connection, sid, timePart, fileExt))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Accept-Length", fmt.Sprintf("%d", len(output)))
	wrote, err := c.Writer.Write([]byte(output))
	log.With("sid", sid).Infof("session downloaded, extension=.%v, output-size=%v, wrote=%v, success=%v, err=%v",
		fileExt, len(output), wrote, err == nil, err)
}

// StreamSessionResult
//
//	@Summary		Stream Session Result
//	@Description	Returns the decoded output of a session as plain text with chunked transfer encoding
//	@Tags			Sessions
//	@Produce		plain
//	@Param			session_id	path		string	true	"The id of the resource"
//	@Param			newline		query		string	false	"Append a newline after each event (1=yes)"
//	@Param			event-time	query		string	false	"Prefix each event with its RFC3339 timestamp (1=yes)"
//	@Param			events		query		string	false	"Comma-separated event types to include: i, o, e (default: o,e)"
//	@Success		200			{string}	string
//	@Failure		403,404,500	{object}	openapi.HTTPError
//	@Router			/sessions/{session_id}/result/stream [get]
func StreamSessionResult(c *gin.Context) {
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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching session")
		return
	}

	if !canAccessSession(ctx, session) {
		c.JSON(http.StatusForbidden, gin.H{"message": "user is not allowed to access this session"})
		return
	}
	if session.UserID != ctx.UserID && !ctx.IsAuditorOrAdminUser() {
		c.JSON(http.StatusForbidden, gin.H{"message": "user is not allowed to stream this session"})
		return
	}

	session.BlobStream, err = session.GetBlobStream()
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching blob stream from session")
		return
	}

	withLineBreak := c.Query("newline") == "1"
	withEventTime := c.Query("event-time") == "1"

	var eventTypes []string
	for _, e := range strings.Split(c.Query("events"), ",") {
		if e == "i" || e == "o" || e == "e" {
			eventTypes = append(eventTypes, e)
		}
	}
	if len(eventTypes) == 0 {
		eventTypes = []string{"o", "e"}
	}

	output, err := parseBlobStream(session, sessionParseOption{
		withLineBreak: withLineBreak,
		withEventTime: withEventTime,
		events:        eventTypes,
	})
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed parsing blob stream")
		return
	}

	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("X-Content-Type-Options", "nosniff")
	if _, err := c.Writer.WriteString(string(output)); err != nil {
		log.With("sid", sessionID).Errorf("failed writing stream response, reason=%v", err)
		return
	}
	c.Writer.Flush()
	log.With("sid", sessionID).Infof("session result streamed, output-size=%v", len(output))
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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to update session metadata: %v", err)
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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching session")
		return
	}

	log.With("user", ctx.UserEmail, "sid", sid).Infof("user initiated a kill process")

	pctx := plugintypes.Context{
		OrgID:             sess.OrgID,
		ConnectionType:    sess.ConnectionType,
		ConnectionSubType: sess.ConnectionSubtype,
		ClientOrigin:      proto.ConnectionOriginClient,
		ClientVerb:        sess.Verb,
		SID:               sid,
	}
	if err := transportsystem.KillSession(pctx, sid); err != nil {
		log.With("sid", sid).Warnf("failed killing session, reason=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusNoContent, nil)
}

func createApprovedReview(ctx *storagev2.Context, session *models.Session, conn *models.Connection, user *models.User, sessionInfo *openapi.ProvisionSession) (bool, error) {
	var accessDuration time.Duration
	if sessionInfo.AccessDurationSec != nil {
		accessDuration = time.Duration(*sessionInfo.AccessDurationSec) * time.Second

		if accessDuration.Hours() > 48 {
			return false, fmt.Errorf("jit access input must not be greater than 48 hours")
		}
	}

	timeWindow, err := reviewapi.ParseTimeWindow(sessionInfo.TimeWindow)
	if err != nil {
		return false, err
	}

	ownerId := ctx.UserID
	apiKeyEmail := ctx.UserEmail
	apiKeyName := ctx.UserName
	now := time.Now().UTC()

	connectionReviewers := conn.Reviewers
	approvedReviewers := sessionInfo.ApprovedReviewers

	// if no reviewers specified in the connection, default to "admin" group
	// to be able to run the connection after approvals
	if len(connectionReviewers) == 0 {
		connectionReviewers = []string{types.GroupAdmin}
		approvedReviewers = []string{types.GroupAdmin}
	}

	// if no approved reviewers specified, auto-approve all reviewers from the connection
	if approvedReviewers == nil {
		approvedReviewers = connectionReviewers
	}

	// populate approved reviewers map for quick lookup
	approvedGroupsMap := make(map[string]struct{})
	for _, rg := range approvedReviewers {
		approvedGroupsMap[rg] = struct{}{}
	}

	areAllGroupsApproved := true
	// prepare review groups
	reviewGroups := []models.ReviewGroups{}
	for _, reviewer := range connectionReviewers {
		group := models.ReviewGroups{
			ID:        uuid.NewString(),
			OrgID:     ctx.OrgID,
			GroupName: reviewer,
			Status:    models.ReviewStatusPending,
		}

		// auto-approve if in the approved reviewers list
		if _, ok := approvedGroupsMap[reviewer]; ok {
			group.Status = models.ReviewStatusApproved
			group.OwnerID = &ownerId
			group.OwnerEmail = &apiKeyEmail
			group.OwnerName = &apiKeyName
			group.ReviewedAt = &now
		} else {
			areAllGroupsApproved = false
		}

		reviewGroups = append(reviewGroups, group)
	}

	// determine review type
	isJitReview := accessDuration > 0
	reviewType := models.ReviewTypeOneTime
	if isJitReview {
		reviewType = models.ReviewTypeJit
	}

	// these values are only used for ad-hoc executions
	var sessionInput string
	var inputEnvVars map[string]string
	var inputClientArgs []string
	if !isJitReview {
		sessionInput = sessionInfo.Script
		inputEnvVars = sessionInfo.EnvVars
		inputClientArgs = sessionInfo.ClientArgs
	}

	// determine review status
	reviewStatus := models.ReviewStatusPending
	if areAllGroupsApproved {
		reviewStatus = models.ReviewStatusApproved
	}

	newRev := &models.Review{
		ID:                uuid.NewString(),
		OrgID:             ctx.OrgID,
		Type:              reviewType,
		SessionID:         session.ID,
		ConnectionName:    conn.Name,
		ConnectionID:      sql.NullString{String: conn.ID, Valid: true},
		AccessDurationSec: int64(accessDuration.Seconds()),
		InputEnvVars:      inputEnvVars,
		InputClientArgs:   inputClientArgs,
		OwnerID:           user.Subject,
		OwnerEmail:        user.Email,
		OwnerName:         &user.Name,
		OwnerSlackID:      &user.SlackID,
		TimeWindow:        timeWindow,
		Status:            reviewStatus,
		ReviewGroups:      reviewGroups,
		CreatedAt:         now,
		RevokedAt:         nil,
	}

	// set revoked at or time window based on review type
	if isJitReview {
		revokedAt := now.Add(accessDuration)
		newRev.RevokedAt = &revokedAt
	}

	log.
		With("sid", session.ID, "id", newRev.ID, "user", ctx.UserID, "org", ctx.OrgID,
			"type", reviewType, "duration", fmt.Sprintf("%vm", accessDuration.Minutes())).
		Infof("creating review")

	if err := models.CreateReview(newRev, sessionInput); err != nil {
		return false, fmt.Errorf("failed saving review: %w", err)
	}

	return areAllGroupsApproved, nil
}

// Provision
//
//	@Summary				Create a provisioned session using API Key
//	@Tags						Sessions
//	@Accept					json
//	@Produce				json
//	@Param					request		body		openapi.ProvisionSession		true	"The request body resource"
//	@Success				200			{object}	openapi.ProvisionSessionResponse	"The session has been created"
//	@Failure				400,422,500	{object}	openapi.HTTPError
//	@Router					/sessions/provision [post]
func Provision(c *gin.Context) {
	sid := uuid.NewString()
	apiroutes.SetSidSpanAttr(c, sid)

	trackClient := analytics.New()
	defer trackClient.Close()

	ctx := storagev2.ParseContext(c)
	var req openapi.ProvisionSession
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if err := CoerceMetadataFields(req.Metadata); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	if err := ValidateCorrelationID(req.CorrelationID); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	// Get user information
	user, err := models.GetUserByEmail(req.UserEmail)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching user %v for exec", ctx.UserEmail)
		return
	}
	if user == nil {
		log.Errorf("user %v not found for exec", ctx.UserEmail)
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("user %v not found for exec", ctx.UserEmail)})
		return
	}

	if user.OrgID != ctx.OrgID {
		log.Errorf("user %v does not belong to org %v for exec", ctx.UserEmail, ctx.OrgID)
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("user %v does not belong to org %v for exec", ctx.UserEmail, ctx.OrgID)})
		return
	}

	// Get connection information
	conn, err := models.GetConnectionByNameOrID(ctx, req.Connection)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetch connection %v for exec, err=%v", req.Connection, err)
		return
	}
	if conn == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("connection %v not found", req.Connection)})
		return
	}

	for key := range req.EnvVars {
		if _, ok := conn.Envs[key]; ok {
			delete(req.EnvVars, key)
		}
	}

	verb := proto.ClientVerbExec
	if req.AccessDurationSec != nil && *req.AccessDurationSec > 0 {
		verb = proto.ClientVerbConnect
	}

	log := log.With("sid", sid, "user", ctx.UserEmail)
	newSession := models.Session{
		ID:                   sid,
		OrgID:                ctx.OrgID,
		Metadata:             req.Metadata,
		IntegrationsMetadata: nil,
		Metrics:              nil,
		BlobInput:            models.BlobInputType(req.Script),
		UserID:               user.Subject,
		UserName:             user.Name,
		UserEmail:            user.Email,
		ConnectionType:       conn.Type,
		ConnectionSubtype:    conn.SubType.String,
		Connection:           conn.Name,
		ConnectionTags:       conn.ConnectionTags,
		Verb:                 verb,
		Status:               string(openapi.SessionStatusOpen),
		ExitCode:             nil,
		CorrelationID:        req.CorrelationID,
		CreatedAt:            time.Now().UTC(),
		EndSession:           nil,
	}

	if conn.JiraIssueTemplateID.String != "" {
		issueTemplate, jiraConfig, err := models.GetJiraIssueTemplatesByID(conn.OrgID, conn.JiraIssueTemplateID.String)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed obtaining jira issue template for %v: %v", conn.Name, err)
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
				httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed parsing jira issue fields: %v", err)
				return
			}
			resp, err := jira.CreateCustomerRequest(issueTemplate, jiraConfig, jiraFields)
			if err != nil {
				httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating jira customer request: %v", err)
				return
			}
			newSession.IntegrationsMetadata = map[string]any{
				"jira_issue_key": resp.IssueKey,
				"jira_issue_url": resp.Links.Agent,
			}
		}
	}

	if err := services.ValidateAndUpsertSession(c, newSession, conn); err != nil {
		log.Errorf("failed creating session, err=%v", err)

		if errors.Is(err, services.ErrMissingMetadata) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
			return
		}

		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating session")
		return
	}
	trackClient.TrackSessionUsageData(analytics.EventSessionCreated, ctx.OrgID, ctx.UserID, sid)

	allGroupsApproved, err := createApprovedReview(ctx, &newSession, conn, user, &req)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating review")
		return
	}

	// if all review groups are approved, set session status to ready
	if allGroupsApproved {
		newSession.Status = string(openapi.SessionStatusReady)

		if err := services.ValidateAndUpsertSession(c, newSession, conn); err != nil {
			log.Errorf("failed updating session, err=%v", err)

			if errors.Is(err, services.ErrMissingMetadata) {
				c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
				return
			}

			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating session")
			return
		}
		trackClient.TrackSessionUsageData(analytics.EventSessionReviewed, ctx.OrgID, ctx.UserID, sid)
	}

	c.JSON(http.StatusAccepted, openapi.ProvisionSessionResponse{
		SessionID: sid,
		UserEmail: user.Email,
		HasReview: !allGroupsApproved,
	})
}
