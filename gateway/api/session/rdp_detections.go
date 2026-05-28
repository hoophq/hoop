package sessionapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/rdp/analyzer"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// rdpAnalysisStatusNotAnalyzed signals that the session has bitmap data but
// no analysis job ever ran (e.g. it predates the feature being enabled).
// The webapp surfaces this with a "Retry analysis" button so the user can
// trigger analysis on demand.
const rdpAnalysisStatusNotAnalyzed = "not_analyzed"

// rdpSessionAnalyzable returns true when a session is eligible to be analyzed:
// it has at least one bitmap frame, Presidio is configured, and the org has
// the experimental flag enabled. This mirrors the gating in
// gateway/rdp/recorder.go that decides whether the recorder enqueues a job
// at session-end so the on-demand retry path uses the same eligibility rules.
func rdpSessionAnalyzable(session *models.Session) bool {
	if session == nil || session.Metrics == nil {
		return false
	}
	count, _ := session.Metrics["bitmap_count"].(float64)
	if count <= 0 {
		return false
	}
	if !analyzer.IsEnabled(appconfig.Get().MSPresidioAnalyzerURL()) {
		return false
	}
	return featureflag.IsEnabled(session.OrgID, analyzer.FlagName)
}

// RDPDetection represents a single PII entity detection with screen-space coordinates.
type RDPDetection struct {
	FrameIndex int     `json:"frame_index"`
	Timestamp  float64 `json:"timestamp"`
	EntityType string  `json:"entity_type"`
	Score      float64 `json:"score"`
	X          int     `json:"x"`
	Y          int     `json:"y"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
}

// RDPAnalysisInfo carries job-level state alongside the detection list so the
// webapp can render progress, retry counts, and the failure reason without
// hitting a second endpoint.
type RDPAnalysisInfo struct {
	// Status mirrors the rdp_analysis_jobs.status column when a job exists,
	// falling back to the sessions.metrics rdp_analysis_status key for sessions
	// that pre-date the jobs table or where analysis is disabled. Possible
	// values: "pending", "running", "analyzing", "done", "failed", or empty
	// when analysis is not configured for the session.
	Status      string     `json:"status"`
	Attempt     int        `json:"attempt"`
	MaxAttempts int        `json:"max_attempts"`
	LastError   string     `json:"last_error,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// RDPDetectionsResponse is the response for the RDP detections endpoint.
type RDPDetectionsResponse struct {
	Detections []RDPDetection `json:"detections"`
	Total      int            `json:"total"`
	// AnalysisStatus is preserved for backwards compatibility with older
	// webapp builds; new code should read .Analysis.Status.
	AnalysisStatus string          `json:"analysis_status"`
	Analysis       RDPAnalysisInfo `json:"analysis"`
}

// GetRDPDetections returns PII entity detections for an RDP session recording.
// Each detection includes screen-space bounding box coordinates for overlay rendering.
//
//	@Summary		Get RDP session PII detections
//	@Description	Returns PII entity detections with screen-space coordinates for an RDP session recording
//	@Tags			Sessions
//	@Param			session_id	path	string	true	"The id of the session"
//	@Produce		json
//	@Success		200		{object}	RDPDetectionsResponse
//	@Failure		400,403,404,500	{object}	openapi.HTTPError
//	@Router			/sessions/{session_id}/rdp-detections [get]
func GetRDPDetections(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	sessionID := c.Param("session_id")

	// Get session
	session, err := models.GetSessionByID(ctx.OrgID, sessionID)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "session not found"})
		return
	case nil:
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching session")
		return
	}

	// Check access
	canAccess := session.UserID == ctx.UserID || ctx.IsAuditorOrAdminUser()
	if !canAccess {
		c.JSON(http.StatusForbidden, gin.H{"message": "user is not allowed to access this session"})
		return
	}

	// Check if it's an RDP session
	if session.ConnectionSubtype != "rdp" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "session is not an RDP recording"})
		return
	}

	// Pull the canonical job state when one exists; fall back to the
	// sessions.metrics field for legacy / pre-jobs-table sessions.
	analysis := RDPAnalysisInfo{MaxAttempts: models.MaxJobAttempts}
	job, err := models.GetRDPAnalysisJobBySessionID(sessionID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching analysis job")
		return
	}
	if job != nil {
		analysis.Status = job.Status
		analysis.Attempt = job.Attempt
		analysis.StartedAt = job.StartedAt
		analysis.FinishedAt = job.FinishedAt
		if job.LastError != nil {
			analysis.LastError = *job.LastError
		}
	}
	// session.metrics keeps a denormalized copy ("pending", "analyzing",
	// "done", "failed"). Use it only when no job row was found so the
	// response always reflects the freshest source of truth.
	if analysis.Status == "" && session.Metrics != nil {
		if raw, ok := session.Metrics["rdp_analysis_status"]; ok {
			if s, ok := raw.(string); ok {
				analysis.Status = s
			}
		}
	}
	// Pre-feature sessions never had a job enqueued and never wrote the
	// metrics key. Surface them as "not_analyzed" so the webapp can offer a
	// Retry button to run analysis on demand. We only flip the status when
	// the session actually has bitmap data and the org has the experimental
	// flag enabled; otherwise the empty status keeps the badge hidden.
	if analysis.Status == "" && rdpSessionAnalyzable(session) {
		analysis.Status = rdpAnalysisStatusNotAnalyzed
	}

	// Fetch detections from database
	dbDetections, err := models.GetRDPEntityDetections(sessionID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching detections")
		return
	}

	// Convert to API response format (omit internal ID and session_id)
	detections := make([]RDPDetection, 0, len(dbDetections))
	for _, d := range dbDetections {
		detections = append(detections, RDPDetection{
			FrameIndex: d.FrameIndex,
			Timestamp:  d.Timestamp,
			EntityType: d.EntityType,
			Score:      d.Score,
			X:          d.X,
			Y:          d.Y,
			Width:      d.Width,
			Height:     d.Height,
		})
	}

	c.JSON(http.StatusOK, RDPDetectionsResponse{
		Detections:     detections,
		Total:          len(detections),
		AnalysisStatus: analysis.Status,
		Analysis:       analysis,
	})
}

// RetryRDPDetections requeues a previously-failed RDP PII analysis job so the
// worker pool will pick it up again. This unblocks sessions whose automatic
// retry budget was exhausted (typically because Presidio was offline for
// longer than the worker pool's poll/backoff window). Returns 200 on success,
// 404 when no failed job exists for the session, and 400 when the job is
// already in a non-failed state (so we don't clobber a run in progress).
//
//	@Summary		Retry RDP PII analysis
//	@Description	Resets a failed RDP analysis job to pending so the worker pool can re-claim it.
//	@Tags			Sessions
//	@Produce		json
//	@Param			id	path		string	true	"Session ID"
//	@Success		200	{object}	RDPDetectionsResponse
//	@Failure		400	{object}	openapi.HTTPError
//	@Failure		403	{object}	openapi.HTTPError
//	@Failure		404	{object}	openapi.HTTPError
//	@Router			/sessions/{id}/rdp-detections/retry [post]
func RetryRDPDetections(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	sessionID := c.Param("session_id")

	session, err := models.GetSessionByID(ctx.OrgID, sessionID)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "session not found"})
		return
	case nil:
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching session")
		return
	}
	canAccess := session.UserID == ctx.UserID || ctx.IsAuditorOrAdminUser()
	if !canAccess {
		c.JSON(http.StatusForbidden, gin.H{"message": "user is not allowed to access this session"})
		return
	}
	if session.ConnectionSubtype != "rdp" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "session is not an RDP recording"})
		return
	}

	job, err := models.GetRDPAnalysisJobBySessionID(sessionID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching analysis job")
		return
	}
	if job == nil {
		// First-time analysis for a pre-feature / never-analyzed session.
		// Refuse if the gateway can't actually run the job (no Presidio,
		// flag off, no bitmap data) so the user doesn't get a job stuck in
		// "pending" forever.
		if !rdpSessionAnalyzable(session) {
			c.JSON(http.StatusBadRequest,
				gin.H{"message": "analysis is not available for this session"})
			return
		}
		if err := models.CreateRDPAnalysisJob(session.OrgID, sessionID); err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to create analysis job")
			return
		}
	} else if job.Status == "failed" {
		if err := models.RetryRDPAnalysisJob(models.DB, job.ID); err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to requeue analysis job")
			return
		}
	} else {
		c.JSON(http.StatusBadRequest,
			gin.H{"message": fmt.Sprintf("cannot retry job in status %q", job.Status)})
		return
	}
	// Mirror the status into sessions.metrics so the audit list / API
	// fallback path also reflects the reset immediately.
	_ = models.UpdateSessionRDPAnalysisStatus(session.OrgID, sessionID, "pending")

	// Re-render the standard payload so the webapp can directly reuse the
	// existing fetch-detections success handler instead of issuing a second
	// GET right after the retry.
	analysis := RDPAnalysisInfo{
		MaxAttempts: models.MaxJobAttempts,
		Status:      "pending",
	}
	c.JSON(http.StatusOK, RDPDetectionsResponse{
		Detections:     []RDPDetection{},
		Total:          0,
		AnalysisStatus: analysis.Status,
		Analysis:       analysis,
	})
}
