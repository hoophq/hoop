package reviewapi

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

var (
	ErrNotFound             = errors.New("review not found")
	ErrWrongState           = errors.New("review is in wrong state")
	ErrNotEligible          = errors.New("not eligible for review")
	ErrSelfApproval         = errors.New("unable to self approve review")
	ErrGroupAlreadyReviewed = errors.New("it was already reviewed")
	ErrForbidden            = errors.New("forbidden")
)

type TransportReleaseConnectionFunc func(orgID, sid, reviewOwnerSlackID, reviewStatus string)

type handler struct {
	TransportReleaseConnection TransportReleaseConnectionFunc
}

func NewHandler(transportReleaseConnectionFn TransportReleaseConnectionFunc) *handler {
	return &handler{transportReleaseConnectionFn}
}

// GetReviewByIdOrSid
//
//	@Summary		Get Review
//	@Description	Get review resource by the id or session id
//	@Tags			Reviews
//	@Param			id	path	string	true	"Resource identifier of the review"
//	@Produce		json
//	@Success		200		{object}	openapi.Review
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/reviews/{id} [get]
func (h *handler) GetByIdOrSid(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	id := c.Param("id")
	review, err := models.GetReviewByIdOrSid(ctx.GetOrgID(), id)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": models.ErrNotFound.Error()})
		return
	case nil:
		c.JSON(http.StatusOK, topOpenApiReview(review))
	default:
		log.Errorf("failed fetching review %v, err=%v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
}

// UpdateReview
//
//	@Summary		Update Review Status
//	@Description	Update the status of a review resource
//	@Tags			Reviews
//	@Param			id	path	string	true	"Resource identifier of the review"
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.ReviewRequest	true	"The request body resource"
//	@Success		200				{object}	openapi.Review
//	@Failure		400,403,404,500	{object}	openapi.HTTPError
//	@Router			/reviews/{id} [put]
func (h *handler) ReviewByIdOrSid(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	reviewIdOrSid := c.Param("id")
	if sid := c.Param("session_id"); sid != "" {
		reviewIdOrSid = sid
		apiroutes.SetSidSpanAttr(c, sid)
	}

	var req openapi.ReviewRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	req.Status = openapi.ReviewRequestStatusType(strings.ToUpper(string(req.Status)))
	rev, err := DoReview(ctx, reviewIdOrSid, models.ReviewStatusType(req.Status))
	switch err {
	case ErrNotEligible, ErrSelfApproval, ErrWrongState:
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
	case ErrForbidden:
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "access denied"})
	case nil:
		if rev.Status == models.ReviewStatusApproved || rev.Status == models.ReviewStatusRejected {
			// release any gRPC connection waiting for a review
			h.TransportReleaseConnection(
				rev.OrgID,
				rev.SessionID,
				ptr.ToString(rev.OwnerSlackID),
				rev.Status.Str(),
			)
		}
		c.JSON(http.StatusOK, topOpenApiReview(rev))
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

// UpdateReviewBySid
//
//	@Summary		Update Review Status By Sid
//	@Description	Update the status of a review resource by the session id
//	@Tags			Reviews
//	@Param			session_id	path	string	true	"Resource identifier of the session"
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.ReviewRequest	true	"The request body resource"
//	@Success		200				{object}	openapi.Review
//	@Failure		400,403,404,500	{object}	openapi.HTTPError
//	@Router			/sessions/{session_id}/review [put]
func (h *handler) ReviewBySid(c *gin.Context) { h.ReviewByIdOrSid(c) }

func DoReview(ctx *storagev2.Context, reviewIdOrSid string, status models.ReviewStatusType) (*models.Review, error) {
	rev, err := models.GetReviewByIdOrSid(ctx.OrgID, reviewIdOrSid)
	switch err {
	case models.ErrNotFound:
		return nil, ErrNotFound
	case nil:
		log.Infof("updating review state, review-id=%v, sid=%v, type=%v, from=%v, to=%v, ctx-user=%v, owner=%v, groups=%v, created-at=%v",
			rev.ID, rev.SessionID, rev.Type, rev.Status, status, ctx.UserEmail,
			rev.OwnerEmail, len(rev.ReviewGroups), rev.CreatedAt.Format(time.RFC3339))
	default:
		return nil, fmt.Errorf("failed obtaining review, err=%v", err)
	}

	switch status {
	case models.ReviewStatusApproved, models.ReviewStatusRejected:
		if rev.Status != models.ReviewStatusPending {
			return nil, ErrWrongState
		}

		if rev.OwnerID == ctx.UserID && !ctx.IsAdmin() {
			return nil, ErrSelfApproval
		}

		reviewsCount := len(rev.ReviewGroups)
		approvedCount := 0

		var isEligibleReviewer bool
		for i, r := range rev.ReviewGroups {
			if slices.Contains(ctx.UserGroups, r.GroupName) {
				// if it contains any group name, it's eligible
				isEligibleReviewer = true

				t := time.Now().UTC()
				rev.ReviewGroups[i].Status = status
				rev.ReviewGroups[i].OwnerID = ptr.String(ctx.UserID)
				rev.ReviewGroups[i].OwnerEmail = ptr.String(ctx.UserEmail)
				rev.ReviewGroups[i].OwnerName = ptr.String(ctx.UserName)
				rev.ReviewGroups[i].OwnerSlackID = ptr.String(ctx.SlackID)
				rev.ReviewGroups[i].ReviewedAt = &t
			}
			if rev.ReviewGroups[i].Status == models.ReviewStatusApproved {
				approvedCount++
			}
		}

		if !isEligibleReviewer {
			return nil, ErrNotEligible
		}

		// when a review is rejected by one user, reject it
		if status == models.ReviewStatusRejected {
			rev.Status = status
		}

		// when all reviews are approved, set the status of the review to approved
		if reviewsCount == approvedCount {
			rev.RevokedAt = func() *time.Time {
				t := time.Now().UTC().Add(time.Duration(rev.AccessDurationSec) * time.Second)
				return &t
			}()
			rev.Status = models.ReviewStatusApproved
		}
	case models.ReviewStatusRevoked:
		if !ctx.IsAdmin() {
			return nil, ErrForbidden
		}
		if rev.Type == models.ReviewTypeJit {
			return nil, ErrNotFound
		}
		if rev.Status != models.ReviewStatusApproved {
			return nil, ErrWrongState
		}
		rev.Status = status
	default:
		return nil, fmt.Errorf("invalid status %q", status)
	}

	if err := models.UpdateReview(rev); err != nil {
		return nil, fmt.Errorf("failed updating review state, err=%v", err)
	}
	return rev, nil
}

func topOpenApiReview(r *models.Review) *openapi.Review {
	if r == nil {
		return nil
	}
	itemGroups := []openapi.ReviewGroup{}
	for _, rg := range r.ReviewGroups {
		var reviewOwner *openapi.ReviewOwner
		if rg.OwnerID != nil {
			reviewOwner = &openapi.ReviewOwner{
				ID:      ptr.ToString(rg.OwnerID),
				Name:    ptr.ToString(rg.OwnerName),
				Email:   ptr.ToString(rg.OwnerEmail),
				SlackID: ptr.ToString(rg.OwnerSlackID),
			}
		}
		itemGroups = append(itemGroups, openapi.ReviewGroup{
			ID:         rg.ID,
			Group:      rg.GroupName,
			Status:     openapi.ReviewRequestStatusType(rg.Status),
			ReviewedBy: reviewOwner,
			ReviewDate: rg.ReviewedAt,
		})
	}
	return &openapi.Review{
		ID:      r.ID,
		Session: r.SessionID,
		Type:    openapi.ReviewType(r.Type),
		// this attribute is saved as seconds
		// but we keep compatibility with clients to show as nano seconds
		AccessDuration:   time.Duration(r.AccessDurationSec) * time.Second,
		Status:           openapi.ReviewStatusType(r.Status),
		RevokeAt:         r.RevokedAt,
		CreatedAt:        r.CreatedAt,
		ReviewGroupsData: itemGroups,
	}
}
