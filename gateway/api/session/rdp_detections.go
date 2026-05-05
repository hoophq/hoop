package sessionapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

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
