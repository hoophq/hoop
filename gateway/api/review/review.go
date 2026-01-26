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
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/hoophq/hoop/gateway/utils"
)

var (
	ErrNotFound             = errors.New("review not found")
	ErrWrongState           = errors.New("review is in wrong state")
	ErrNotEligible          = errors.New("not eligible for review")
	ErrSelfApproval         = errors.New("unable to self approve review")
	ErrGroupAlreadyReviewed = errors.New("it was already reviewed")
	ErrForbidden            = errors.New("forbidden")
	ErrUnknownStatus        = errors.New("unknown status")
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
		c.JSON(http.StatusOK, toOpenApiReview(review))
	default:
		log.Errorf("failed fetching review %v, err=%v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
}

// List
//
//	@Summary		Get Review List,
//	@Description	Get all reviews resource
//	@Tags			Reviews
//	@Produce		json
//	@Success		200		{object}	[]openapi.Review
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/reviews [get]
func (h *handler) List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	reviews, err := models.ListReviews(ctx.GetOrgID())

	if err != nil {
		log.Errorf("failed fetching reviews, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	openapiReviews := []openapi.Review{}
	for _, r := range *reviews {
		openapiReviews = append(openapiReviews, *toOpenApiReview(&r))
	}

	c.JSON(http.StatusOK, openapiReviews)
}

func ParseTimeWindow(timeWindow *openapi.ReviewSessionTimeWindow) (*models.ReviewTimeWindow, error) {
	if timeWindow == nil {
		return nil, nil
	}

	startTime := timeWindow.Configuration["start_time"]
	endTime := timeWindow.Configuration["end_time"]

	if startTime == "" || endTime == "" {
		return nil, fmt.Errorf("both from and to time must be provided")
	}

	_, err := time.Parse("15:04", startTime)
	if err != nil {
		return nil, fmt.Errorf("invalid start_time format, expected HH:MM in 24-hour format")
	}

	_, err = time.Parse("15:04", endTime)
	if err != nil {
		return nil, fmt.Errorf("invalid end_time format, expected HH:MM in 24-hour format")
	}

	return &models.ReviewTimeWindow{
		Type:          string(timeWindow.Type),
		Configuration: timeWindow.Configuration,
	}, nil
}

// UpdateReview
//
//	@Summary				Update Review Status
//	@description.markdown	api-update-review
//	@Tags					Reviews
//	@Param					id	path	string	true	"Resource identifier of the review"
//	@Accept					json
//	@Produce				json
//	@Param					request			body		openapi.ReviewRequest	true	"The request body resource"
//	@Success				200				{object}	openapi.Review
//	@Failure				400,403,404,500	{object}	openapi.HTTPError
//	@Router					/reviews/{id} [put]
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

	// Validate time window if provided
	reviewTimeWindow, err := ParseTimeWindow(req.TimeWindow)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	req.Status = openapi.ReviewRequestStatusType(strings.ToUpper(string(req.Status)))
	rev, err := DoReview(ctx, reviewIdOrSid, models.ReviewStatusType(req.Status), reviewTimeWindow, req.ForceReview)
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
		c.JSON(http.StatusOK, toOpenApiReview(rev))
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

// UpdateReviewBySid
//
//	@Summary				Update Review Status By Sid
//	@description.markdown	api-update-review
//	@Tags					Reviews
//	@Param					session_id	path	string	true	"Resource identifier of the session"
//	@Accept					json
//	@Produce				json
//	@Param					request			body		openapi.ReviewRequest	true	"The request body resource"
//	@Success				200				{object}	openapi.Review
//	@Failure				400,403,404,500	{object}	openapi.HTTPError
//	@Router					/sessions/{session_id}/review [put]
func (h *handler) ReviewBySid(c *gin.Context) { h.ReviewByIdOrSid(c) }

// DoReview updates the status of a review identified by reviewIdOrSid. The hasForced parameter
// indicates whether the review status change was forced by an administrator or privileged user,
// bypassing normal review validation rules or approval workflows.
func DoReview(ctx *storagev2.Context, reviewIdOrSid string, status models.ReviewStatusType, timeWindow *models.ReviewTimeWindow, hasForced bool) (*models.Review, error) {
	rev, err := models.GetReviewByIdOrSid(ctx.OrgID, reviewIdOrSid)
	switch err {
	case models.ErrNotFound:
		return nil, ErrNotFound
	case nil:
		log.Infof("updating review state, review-id=%v, sid=%v, type=%v, from=%v, to=%v, forced=%v, ctx-user=%v, owner=%v, groups=%v, created-at=%v",
			rev.ID, rev.SessionID, rev.Type, rev.Status, status, hasForced, ctx.UserEmail,
			rev.OwnerEmail, len(rev.ReviewGroups), rev.CreatedAt.Format(time.RFC3339))
	default:
		return nil, fmt.Errorf("failed obtaining review, err=%v", err)
	}

	connection, err := models.GetConnectionByNameOrID(ctx, rev.ConnectionName)
	if connection == nil || err != nil {
		return nil, fmt.Errorf("failed fetching connection for forced review, err=%v", err)
	}

	if timeWindow != nil {
		if rev.TimeWindow != nil {
			return nil, fmt.Errorf("time window can only be set once")
		}

		if status == models.ReviewStatusApproved {
			rev.TimeWindow = timeWindow
		}
	}

	rev, err = doReview(ctx, rev, connection, status, hasForced)
	if err != nil {
		return nil, err
	}

	if err := models.UpdateReview(rev); err != nil {
		return nil, fmt.Errorf("failed updating review state, reason=%v", err)
	}
	return rev, nil
}

func doReview(ctx *storagev2.Context, rev *models.Review, connection *models.Connection, status models.ReviewStatusType, force bool) (*models.Review, error) {
	err := validateReviewStatusTransition(ctx, rev, status)
	if err != nil {
		return nil, err
	}

	if force {
		rev, err = doForcedReview(ctx, rev, connection, status)
	} else {
		rev, err = doIndividualReview(ctx, rev, connection, status)
	}

	if err != nil {
		return nil, err
	}

	if rev.Status == models.ReviewStatusApproved {
		// TODO(san): should it be set only for jit reviews?
		expiration := time.Now().UTC().Add(time.Duration(rev.AccessDurationSec) * time.Second)
		rev.RevokedAt = &expiration
	}

	return rev, nil
}

func doForcedReview(ctx *storagev2.Context, rev *models.Review, connection *models.Connection, status models.ReviewStatusType) (*models.Review, error) {
	// check if the user has permissions to force the review
	forceGroupFound := utils.SlicesFindFirstIntersection(ctx.UserGroups, connection.ForceApproveGroups)
	if forceGroupFound == nil {
		return nil, ErrNotEligible
	}

	// update review group to
	forceGroupIndex := slices.IndexFunc(rev.ReviewGroups, func(rg models.ReviewGroups) bool {
		return rg.GroupName == *forceGroupFound
	})

	reviewedAt := time.Now().UTC()
	// mutate or append the review group for the forced approver
	if forceGroupIndex != -1 {
		rev.ReviewGroups[forceGroupIndex].Status = status
		rev.ReviewGroups[forceGroupIndex].OwnerID = &ctx.UserID
		rev.ReviewGroups[forceGroupIndex].OwnerEmail = &ctx.UserEmail
		rev.ReviewGroups[forceGroupIndex].OwnerName = &ctx.UserName
		rev.ReviewGroups[forceGroupIndex].OwnerSlackID = &ctx.SlackID
		rev.ReviewGroups[forceGroupIndex].ReviewedAt = &reviewedAt
		rev.ReviewGroups[forceGroupIndex].ForcedReview = true
	} else {
		rev.ReviewGroups = append(rev.ReviewGroups, models.ReviewGroups{
			OrgID:        ctx.OrgID,
			ID:           uuid.NewString(),
			ReviewID:     rev.ID,
			GroupName:    *forceGroupFound,
			Status:       status,
			OwnerID:      &ctx.UserID,
			OwnerEmail:   &ctx.UserEmail,
			OwnerName:    &ctx.UserName,
			OwnerSlackID: &ctx.SlackID,
			ReviewedAt:   &reviewedAt,
			ForcedReview: true,
		})
	}

	rev.Status = status

	return rev, nil
}

func doIndividualReview(ctx *storagev2.Context, rev *models.Review, connection *models.Connection, status models.ReviewStatusType) (*models.Review, error) {
	reviewedAt := time.Now().UTC()
	approvedCount := 0
	reviewsCountNeeded := len(rev.ReviewGroups)
	if connection.MinReviewApprovals != nil {
		reviewsCountNeeded = min(reviewsCountNeeded, *connection.MinReviewApprovals)
	}

	var isEligibleReviewer bool
	for i, r := range rev.ReviewGroups {
		// if it contains any group name, it's eligible
		if slices.Contains(ctx.UserGroups, r.GroupName) {
			isEligibleReviewer = true

			rev.ReviewGroups[i].Status = status
			rev.ReviewGroups[i].OwnerID = ptr.String(ctx.UserID)
			rev.ReviewGroups[i].OwnerEmail = ptr.String(ctx.UserEmail)
			rev.ReviewGroups[i].OwnerName = ptr.String(ctx.UserName)
			rev.ReviewGroups[i].OwnerSlackID = ptr.String(ctx.SlackID)
			rev.ReviewGroups[i].ReviewedAt = &reviewedAt
		}

		// count approved reviews
		if rev.ReviewGroups[i].Status == models.ReviewStatusApproved {
			approvedCount++
		}
	}

	// allow review owner and admins to deny the review
	// even if they're not eligible to perform the review
	if !isEligibleReviewer {
		isOwnerOrAdmin := rev.OwnerID == ctx.UserID || ctx.IsAdmin()
		if !isOwnerOrAdmin {
			return nil, ErrNotEligible
		}

		isDeniedStatus := status == models.ReviewStatusRejected || status == models.ReviewStatusRevoked
		if isDeniedStatus {
			var groupName string
			if ctx.IsAdmin() {
				groupName = types.GroupAdmin
			} else if len(ctx.UserGroups) > 0 {
				groupName = ctx.UserGroups[0]
			}

			rev.ReviewGroups = append(rev.ReviewGroups,
				models.ReviewGroups{
					OrgID:        ctx.OrgID,
					ID:           uuid.NewString(),
					ReviewID:     rev.ID,
					GroupName:    groupName,
					Status:       status,
					OwnerID:      ptr.String(ctx.UserID),
					OwnerEmail:   ptr.String(ctx.UserEmail),
					OwnerName:    ptr.String(ctx.UserName),
					OwnerSlackID: ptr.String(ctx.SlackID),
					ReviewedAt:   &reviewedAt,
				},
			)
		}
	}

	// if all reviews are approved, set the review status to approved as well
	// check if status is approved to avoid approving a rejected review
	// Update the overall review status based on individual review group statuses
	if status == models.ReviewStatusApproved {
		// Only approve the review if all required review groups have approved
		if approvedCount == reviewsCountNeeded {
			rev.Status = models.ReviewStatusApproved
		}
		// Otherwise, keep status as pending (no explicit assignment needed)
	} else {
		// For rejected or revoked status, update immediately
		rev.Status = status
	}

	return rev, nil
}

func validateReviewStatusTransition(ctx *storagev2.Context, rev *models.Review, status models.ReviewStatusType) error {
	// user can only approve, reject or revoke a review
	switch status {
	case models.ReviewStatusApproved, models.ReviewStatusRejected, models.ReviewStatusRevoked:
	default:
		return ErrUnknownStatus
	}

	// check if the review is in a state that allows to proceed
	// it may be pending or already approved (so it can be revoked)
	if rev.Status != models.ReviewStatusPending && rev.Status != models.ReviewStatusApproved {
		return ErrWrongState
	}

	// user can't self approve their own review, only deny
	isResourceOwner := rev.OwnerID == ctx.UserID
	if status == models.ReviewStatusApproved && isResourceOwner && !ctx.IsAdmin() {
		return ErrSelfApproval
	}

	// it can only revoke in the approved status and jit types
	if status == models.ReviewStatusRevoked {
		if rev.Status != models.ReviewStatusApproved {
			return ErrWrongState
		}

		if rev.Type != models.ReviewTypeJit {
			return ErrNotFound
		}
	}

	return nil
}

func toOpenApiReview(r *models.Review) *openapi.Review {
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
			ID:           rg.ID,
			Group:        rg.GroupName,
			Status:       openapi.ReviewRequestStatusType(rg.Status),
			ReviewedBy:   reviewOwner,
			ReviewDate:   rg.ReviewedAt,
			ForcedReview: rg.ForcedReview,
		})
	}
	var timeWindow *openapi.ReviewSessionTimeWindow
	if r.TimeWindow != nil {
		timeWindow = &openapi.ReviewSessionTimeWindow{
			Type:          openapi.ReviewTimeWindowType(r.TimeWindow.Type),
			Configuration: r.TimeWindow.Configuration,
		}
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
		TimeWindow:       timeWindow,
	}
}
