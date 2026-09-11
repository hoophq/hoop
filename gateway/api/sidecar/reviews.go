package apisidecar

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

const (
	// reviewOwnerEmail stands in for the requester a sidecar review does not
	// have. The control plane registers no ordinary users, so nothing here
	// identifies a person. Known debt: every sidecar review shows the same
	// requester until there is something real to record, which is where the
	// database user or the source address behind the statement will go.
	reviewOwnerEmail = "hoop@hoop.dev"

	// reviewConnectionType is what a session must declare. private.sessions
	// requires one and enum_connection_type has no label for a listener, so a
	// sidecar session takes the catch-all rather than the enum taking a new
	// value it would carry forever.
	reviewConnectionType = "custom"
)

// PostReview
//
//	@Summary		Create Sidecar Review
//	@Description	Register a review for a statement a sidecar held. The sidecar is taken from the token, never the body.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			hoop-sidecar-token	header		string							true	"The token returned when the sidecar was created"
//	@Param			request				body		openapi.SidecarReviewRequest	true	"The request body resource"
//	@Success		201					{object}	openapi.Review
//	@Failure		400,401,412,500		{object}	openapi.HTTPError
//	@Router			/sidecars/reviews [post]
func PostReview(c *gin.Context) {
	sidecar := apiroutes.SidecarFromContext(c)
	if sidecar == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}

	// A gateway would take the review and then never be able to settle it:
	// approval there resolves a connection, and this review has none. Refuse
	// rather than write a row nobody can act on. Checked after authentication,
	// so an unauthenticated caller learns nothing about the deployment.
	if !appconfig.Get().IsControlPlane() {
		c.JSON(http.StatusPreconditionFailed, gin.H{
			"message": "sidecar reviews are served by the control plane",
		})
		return
	}

	var req openapi.SidecarReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	// Decoded here so a human never sees base64. The statement is stored as
	// text and read back verbatim into the Slack message a reviewer approves
	// from, and nothing further down this path decodes anything.
	statement, err := base64.StdEncoding.DecodeString(req.Payload)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("payload is not valid base64: %v", err)})
		return
	}

	rev, err := createSidecarReview(sidecar, req.ListenerName, string(statement))
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating sidecar review: %v", err)
		return
	}

	log.With("sid", rev.SessionID, "review-id", rev.ID, "sidecar", sidecar.Name, "listener", req.ListenerName).
		Infof("registered a sidecar review")

	// TrackEvent, not TrackRequest: this request has no user, and TrackRequest
	// answers an empty user email by doing nothing at all.
	analytics.New().TrackEvent(analytics.EventCreateSidecarReview, map[string]any{
		"org-id":   sidecar.OrgID,
		"sidecar":  sidecar.Name,
		"listener": req.ListenerName,
	})

	c.JSON(http.StatusCreated, toOpenApiSidecarReview(rev))
}

// createSidecarReview writes the session and the review one statement needs to
// wait for a human.
func createSidecarReview(sidecar *models.Sidecar, listenerName, statement string) (*models.Review, error) {
	now := time.Now().UTC()
	sessionID := uuid.NewString()

	// A review is one per session (private.reviews is UNIQUE on org and
	// session), and UpdateReview syncs the session's status when the review
	// settles, so the session is not optional bookkeeping.
	err := models.UpsertSession(models.Session{
		ID:             sessionID,
		OrgID:          sidecar.OrgID,
		BlobInput:      models.BlobInputType(statement),
		Connection:     "",
		ConnectionType: reviewConnectionType,
		Verb:           pb.ClientVerbExec,
		Status:         string(openapi.SessionStatusOpen),
		UserID:         sidecar.ID,
		UserName:       sidecar.Name,
		UserEmail:      reviewOwnerEmail,
		CreatedAt:      now,
	})
	if err != nil {
		return nil, fmt.Errorf("failed creating session: %w", err)
	}

	rev := &models.Review{
		ID:        uuid.NewString(),
		OrgID:     sidecar.OrgID,
		Type:      models.ReviewTypeOneTime,
		Status:    models.ReviewStatusPending,
		SessionID: sessionID,

		// The listener this statement arrived on, in place of a connection.
		// connection_name is NOT NULL and stays empty: a sidecar review never
		// resolves one, and a listener name in that column could collide with
		// a real connection of the same name.
		SidecarID:    sql.NullString{String: sidecar.ID, Valid: true},
		ListenerName: sql.NullString{String: listenerName, Valid: listenerName != ""},

		OwnerID:    sidecar.ID,
		OwnerEmail: reviewOwnerEmail,
		OwnerName:  ptr.String(sidecar.Name),

		MinApprovals: ptr.Int(1),
		ReviewGroups: eligibleReviewGroups(sidecar.OrgID),

		CreatedAt: now,
	}

	if err := models.CreateReview(rev, statement); err != nil {
		return nil, fmt.Errorf("failed creating review: %w", err)
	}
	return rev, nil
}

// eligibleReviewGroups is the fixed policy: an admin or an approver, whichever
// gets there first. Read from types rather than spelled, because an
// organization can rename its admin group through the environment and a
// hardcoded name would produce a row nobody is in.
func eligibleReviewGroups(orgID string) []models.ReviewGroups {
	groups := []string{types.GroupAdmin, types.GroupApprover}
	reviewGroups := make([]models.ReviewGroups, 0, len(groups))
	for _, name := range groups {
		reviewGroups = append(reviewGroups, models.ReviewGroups{
			ID:        uuid.NewString(),
			OrgID:     orgID,
			GroupName: name,
			Status:    models.ReviewStatusPending,
		})
	}
	return reviewGroups
}

// toOpenApiSidecarReview renders what the sidecar needs to recognise the review
// later. The connection fields are left out rather than sent empty: this review
// has none, and an empty string invites a client to read meaning into it.
func toOpenApiSidecarReview(r *models.Review) *openapi.Review {
	groups := make([]openapi.ReviewGroup, 0, len(r.ReviewGroups))
	for _, rg := range r.ReviewGroups {
		groups = append(groups, openapi.ReviewGroup{
			ID:     rg.ID,
			Group:  rg.GroupName,
			Status: openapi.ReviewRequestStatusType(rg.Status),
		})
	}
	return &openapi.Review{
		ID:               r.ID,
		Session:          r.SessionID,
		Type:             openapi.ReviewType(r.Type),
		Status:           openapi.ReviewStatusType(r.Status),
		CreatedAt:        r.CreatedAt,
		ReviewGroupsData: groups,
		MinApprovals:     r.MinApprovals,
		SidecarID:        ptr.String(r.SidecarID.String),
		ListenerName:     ptr.String(r.ListenerName.String),
	}
}
