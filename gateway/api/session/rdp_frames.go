package sessionapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// RDPFrame represents a single bitmap frame from an RDP session
type RDPFrame struct {
	Index     int             `json:"index"`
	Timestamp float64         `json:"timestamp"`
	Bitmap    json.RawMessage `json:"bitmap"`
}

// RDPFramesResponse is the paginated response for RDP frames
type RDPFramesResponse struct {
	Frames        []RDPFrame `json:"frames"`
	TotalFrames   int        `json:"total_frames"`
	TotalDuration float64    `json:"total_duration"`
	Offset        int        `json:"offset"`
	Limit         int        `json:"limit"`
	HasMore       bool       `json:"has_more"`
}

// GetRDPFrames returns paginated bitmap frames from an RDP session
//
//	@Summary		Get RDP session frames
//	@Description	Returns paginated bitmap frames from an RDP session recording
//	@Tags			Sessions
//	@Param			session_id	path	string	true	"The id of the session"
//	@Param			offset		query	int		false	"Offset for pagination"	default(0)
//	@Param			limit		query	int		false	"Limit number of frames"	default(50)
//	@Produce		json
//	@Success		200		{object}	RDPFramesResponse
//	@Failure		400,403,404,500	{object}	openapi.HTTPError
//	@Router			/sessions/{session_id}/rdp-frames [get]
func GetRDPFrames(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	sessionID := c.Param("session_id")

	// Parse pagination parameters
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	if offset < 0 {
		offset = 0
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}

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

	// Get blob stream
	blob, err := session.GetBlobStream()
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching session data")
		return
	}
	if blob == nil || len(blob.BlobStream) == 0 {
		c.JSON(http.StatusOK, RDPFramesResponse{
			Frames:      []RDPFrame{},
			TotalFrames: 0,
			Offset:      offset,
			Limit:       limit,
			HasMore:     false,
		})
		return
	}

	// Parse the blob stream (it's a JSON array of events)
	var events [][]interface{}
	if err := json.Unmarshal(blob.BlobStream, &events); err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed parsing session data")
		return
	}

	// Filter bitmap events (type "b")
	var bitmapFrames []RDPFrame
	frameIndex := 0
	for _, event := range events {
		if len(event) < 3 {
			continue
		}
		eventType, ok := event[1].(string)
		if !ok || eventType != "b" {
			continue
		}

		// Get timestamp
		timestamp, _ := event[0].(float64)

		// Get bitmap data (base64 encoded JSON)
		bitmapB64, ok := event[2].(string)
		if !ok {
			continue
		}

		// Decode base64 to get the JSON bitmap data
		bitmapJSON, err := base64.StdEncoding.DecodeString(bitmapB64)
		if err != nil {
			log.Debugf("failed decoding bitmap base64: %v", err)
			continue
		}

		bitmapFrames = append(bitmapFrames, RDPFrame{
			Index:     frameIndex,
			Timestamp: timestamp,
			Bitmap:    json.RawMessage(bitmapJSON),
		})
		frameIndex++
	}

	totalFrames := len(bitmapFrames)

	// Get total duration from the last frame's timestamp
	var totalDuration float64
	if totalFrames > 0 {
		totalDuration = bitmapFrames[totalFrames-1].Timestamp
	}

	// Apply pagination
	start := offset
	if start > totalFrames {
		start = totalFrames
	}
	end := start + limit
	if end > totalFrames {
		end = totalFrames
	}

	paginatedFrames := bitmapFrames[start:end]

	c.JSON(http.StatusOK, RDPFramesResponse{
		Frames:        paginatedFrames,
		TotalFrames:   totalFrames,
		TotalDuration: totalDuration,
		Offset:        offset,
		Limit:         limit,
		HasMore:       end < totalFrames,
	})
}
