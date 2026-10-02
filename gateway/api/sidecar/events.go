package apisidecar

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/daemon"
)

// PostEvents
//
//	@Summary		Record Sidecar Session Events
//	@Description	Record a sidecar's audit events as sessions. The sidecar is taken from the token, never the body, and every session it writes is its own.
//	@Description	2xx means the batch is applied, now or by an earlier request: an event at or below its session's last applied seq is ignored. 4xx means the sidecar must not resend the batch. 5xx means it may resend it as it is.
//	@Description	The organization must have the experimental.sidecar_session_events flag on; the handshake answers the hoop-sidecar-session-events header when it does.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			hoop-sidecar-token	header		string								true	"The token returned when the sidecar was created"
//	@Param			request				body		openapi.SidecarSessionEventsRequest	true	"The request body resource"
//	@Success		200					{object}	openapi.SidecarSessionEventsResponse
//	@Failure		400,401,403,412,413,422,500	{object}	openapi.HTTPError
//	@Router			/sidecars/events [post]
func PostEvents(c *gin.Context) {
	sidecar := apiroutes.SidecarFromContext(c)
	if sidecar == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}
	// Checked per request, not only at the handshake: the flag can go off
	// between two heartbeats, and the 412 stops the sidecar at once.
	if !featureflag.IsEnabled(sidecar.OrgID, services.SidecarSessionEventsFlag) {
		c.JSON(http.StatusPreconditionFailed, gin.H{
			"message": "this organization does not record sidecar sessions; " +
				"turn on " + services.SidecarSessionEventsFlag + " in Settings -> Experimental",
		})
		return
	}

	if c.Request.ContentLength > daemon.MaxSessionEventsBatchBytes {
		answerBatchTooLarge(c)
		return
	}
	// One byte past the bound, so an oversized body without a length is
	// refused as what it is rather than decoded from a truncated document.
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, daemon.MaxSessionEventsBatchBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("reading the body: %v", err)})
		return
	}
	if len(body) > daemon.MaxSessionEventsBatchBytes {
		answerBatchTooLarge(c)
		return
	}
	var req daemon.SessionEventsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("the body is not a batch of events: %v", err)})
		return
	}
	if len(req.Events) > daemon.MaxSessionEventsBatch {
		answerBatchTooLarge(c)
		return
	}
	if err := services.ValidateSidecarSessionEvents(req.Events); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	result, err := services.ApplySidecarSessionEvents(models.DB, sidecar, req.Events)
	if err != nil {
		var refused services.SidecarEventsRefused
		if errors.As(err, &refused) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": refused.Error()})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed recording the sidecar session events")
		return
	}
	c.JSON(http.StatusOK, openapi.SidecarSessionEventsResponse{
		Accepted:   result.Accepted,
		Duplicates: result.Duplicates,
	})
}

func answerBatchTooLarge(c *gin.Context) {
	c.JSON(http.StatusRequestEntityTooLarge, gin.H{
		"message": fmt.Sprintf("a batch carries at most %d events and %d bytes",
			daemon.MaxSessionEventsBatch, daemon.MaxSessionEventsBatchBytes),
	})
}
