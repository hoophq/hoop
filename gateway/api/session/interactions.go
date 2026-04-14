package sessionapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const defaultInteractionsLimit = 50
const maxInteractionsLimit = 100

type interactionResponse struct {
	ID        string          `json:"id"`
	Sequence  int             `json:"sequence"`
	Input     string          `json:"input,omitempty"`
	Output    json.RawMessage `json:"output"`
	ExitCode  *int            `json:"exit_code"`
	CreatedAt string          `json:"created_at"`
	EndedAt   *string         `json:"ended_at,omitempty"`
}

type listInteractionsResponse struct {
	Interactions []interactionResponse `json:"interactions"`
	HasMore      bool                  `json:"has_more"`
}

// ListInteractions returns the list of interactions for a session.
//
//	@Summary	List Session Interactions
//	@Description	Returns the list of interactions for a machine session, with support for pagination via the sequence parameter.
//	@Tags			Sessions
//	@Produce		json
//	@Param			session_id		path		string	true	"The session ID"
//	@Param			after_sequence	query		int		false	"Only return interactions with sequence > N"
//	@Param			limit			query		int		false	"Max interactions to return (default: 50, max: 100)"
//	@Success		200				{object}	listInteractionsResponse
//	@Failure		400,403,404,500	{object}	openapi.HTTPError
//	@Router			/sessions/{session_id}/interactions [get]
func ListInteractions(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	sessionID := c.Param("session_id")

	session, err := models.GetSessionByID(ctx.OrgID, sessionID)

	if errors.Is(err, models.ErrNotFound) {
		httputils.AbortWithErr(c, http.StatusNotFound, err, "session not found")
		return
	}

	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching session")
		return
	}

	if !canAccessSession(ctx, session) {
		c.JSON(http.StatusForbidden, gin.H{"message": "user is not allowed to access this session"})
		return
	}

	afterSequence, _ := strconv.Atoi(c.DefaultQuery("after_sequence", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(defaultInteractionsLimit)))
	if limit <= 0 || limit > maxInteractionsLimit {
		limit = defaultInteractionsLimit
	}

	// fetch one extra to determine has_more
	interactions, err := models.ListSessionInteractions(models.DB, ctx.OrgID, sessionID, afterSequence, limit+1)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing session interactions")
		return
	}

	hasMore := len(interactions) > limit
	if hasMore {
		interactions = interactions[:limit]
	}

	items := make([]interactionResponse, 0, len(interactions))
	for _, interaction := range interactions {
		item := interactionResponse{
			ID:        interaction.ID,
			Sequence:  interaction.Sequence,
			ExitCode:  interaction.ExitCode,
			CreatedAt: interaction.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if interaction.EndedAt != nil {
			s := interaction.EndedAt.UTC().Format("2006-01-02T15:04:05Z")
			item.EndedAt = &s
		}

		// fetch input blob
		if interaction.BlobInputID != nil {
			input, err := models.GetInteractionBlobInput(models.DB, ctx.OrgID, *interaction.BlobInputID)
			if err != nil {
				httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching interaction input")
				return
			}
			item.Input = string(input)
		}

		// fetch stream blob
		if interaction.BlobStreamID != nil {
			blob, err := models.GetInteractionBlobStream(models.DB, ctx.OrgID, *interaction.BlobStreamID)
			if err != nil {
				httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching interaction stream")
				return
			}
			if blob != nil {
				item.Output = blob.BlobStream
			}
		}
		if item.Output == nil {
			item.Output = json.RawMessage(`[]`)
		}

		items = append(items, item)
	}

	c.JSON(http.StatusOK, listInteractionsResponse{
		Interactions: items,
		HasMore:      hasMore,
	})
}
