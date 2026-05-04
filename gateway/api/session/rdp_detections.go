package sessionapi

import (
	"net/http"

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

// RDPDetectionsResponse is the response for the RDP detections endpoint.
type RDPDetectionsResponse struct {
	Detections     []RDPDetection `json:"detections"`
	Total          int            `json:"total"`
	AnalysisStatus string         `json:"analysis_status"`
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

	// Get analysis status from session metrics
	analysisStatus := ""
	if session.Metrics != nil {
		if status, ok := session.Metrics["rdp_analysis_status"]; ok {
			if s, ok := status.(string); ok {
				analysisStatus = s
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
		AnalysisStatus: analysisStatus,
	})
}
