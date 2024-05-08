package review

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgreview "github.com/runopsio/hoop/gateway/pgrest/review"
	pgsession "github.com/runopsio/hoop/gateway/pgrest/session"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type (
	Service struct {
		TransportService transportService
	}

	transportService interface {
		ReviewStatusChange(rev *types.Review)
	}
)

var (
	ErrNotFound     = errors.New("review not found")
	ErrWrongState   = errors.New("review in wrong state")
	ErrNotEligible  = errors.New("not eligible for review")
	ErrSelfApproval = errors.New("unable to self approve review")
)

const (
	ReviewTypeJit     = "jit"
	ReviewTypeOneTime = "onetime"
)

func (s *Service) FindOne(ctx pgrest.OrgContext, id string) (*types.Review, error) {
	return pgreview.New().FetchOneByID(ctx, id)
}

// FindOneTimeReview returns an one time review by session id
func (s *Service) FindBySessionID(ctx pgrest.Context, sessionID string) (*types.Review, error) {
	return pgreview.New().FetchOneBySid(ctx, sessionID)
}

func (s *Service) Persist(ctx pgrest.OrgContext, review *types.Review) error {
	if review.Id == "" {
		review.Id = uuid.NewString()
	}

	for i, r := range review.ReviewGroupsData {
		if r.Id == "" {
			review.ReviewGroupsData[i].Id = uuid.NewString()
		}
	}

	parsedReview := &types.Review{
		Id:               review.Id,
		CreatedAt:        review.CreatedAt,
		OrgId:            review.OrgId,
		Type:             review.Type,
		Session:          review.Session,
		Connection:       review.Connection,
		ConnectionId:     review.Connection.Id,
		CreatedBy:        review.ReviewOwner.Id,
		ReviewOwner:      review.ReviewOwner,
		Input:            review.Input,
		InputEnvVars:     review.InputEnvVars,
		InputClientArgs:  review.InputClientArgs,
		AccessDuration:   review.AccessDuration,
		RevokeAt:         review.RevokeAt,
		Status:           review.Status,
		ReviewGroupsIds:  review.ReviewGroupsIds,
		ReviewGroupsData: review.ReviewGroupsData,
	}

	if err := pgreview.New().Upsert(parsedReview); err != nil {
		return err
	}
	return nil
}

// FindApprovedJitReviews returns jit reviews that are active based on the access duration
func (s *Service) FindApprovedJitReviews(ctx pgrest.OrgContext, connID string) (*types.Review, error) {
	return pgreview.New().FetchJit(ctx, connID)
}

func (s *Service) Revoke(ctx pgrest.OrgContext, reviewID string) (*types.Review, error) {
	rev, err := s.FindOne(ctx, reviewID)
	if err != nil {
		return nil, err
	}
	// non-jit type reviews cannot be revoked
	if rev == nil || rev.Type != ReviewTypeJit {
		return nil, ErrNotFound
	}
	// only approved reviews could be revoked
	if rev.Status != types.ReviewStatusApproved {
		return nil, ErrWrongState
	}
	rev.Status = types.ReviewStatusRevoked
	return rev, s.Persist(ctx, rev)
}

// called by slack plugin and webapp
func (s *Service) Review(ctx *storagev2.Context, reviewID string, status types.ReviewStatus) (*types.Review, error) {
	rev, err := s.FindOne(ctx, reviewID)
	if err != nil {
		return nil, fmt.Errorf("fetch review error: %v", err)
	}
	if rev == nil {
		return nil, ErrNotFound
	}
	if rev.Status != types.ReviewStatusPending {
		return rev, ErrWrongState
	}
	if rev.ReviewOwner.Id == ctx.UserID && !ctx.IsAdmin() {
		return nil, ErrSelfApproval
	}

	isEligibleReviewer := false
	for _, r := range rev.ReviewGroupsData {
		if pb.IsInList(r.Group, ctx.UserGroups) {
			isEligibleReviewer = true
			break
		}
	}
	if !isEligibleReviewer {
		return nil, ErrNotEligible
	}

	reviewsCount := len(rev.ReviewGroupsData)
	approvedCount := 0

	if status == types.ReviewStatusRejected {
		rev.Status = status
	}

	for i, r := range rev.ReviewGroupsData {
		if pb.IsInList(r.Group, ctx.UserGroups) {
			t := time.Now().UTC().Format(time.RFC3339)
			rev.ReviewGroupsData[i].Status = status
			rev.ReviewGroupsData[i].ReviewedBy = &types.ReviewOwner{
				Id:    ctx.UserID,
				Name:  ctx.UserName,
				Email: ctx.UserEmail,
			}
			rev.ReviewGroupsData[i].ReviewDate = &t
		}
		if rev.ReviewGroupsData[i].Status == types.ReviewStatusApproved {
			approvedCount++
		}
	}

	if reviewsCount == approvedCount {
		rev.RevokeAt = func() *time.Time { t := time.Now().UTC().Add(rev.AccessDuration); return &t }()
		rev.Status = types.ReviewStatusApproved
	}

	if err := s.Persist(ctx, rev); err != nil {
		return nil, fmt.Errorf("saving review error: %v", err)
	}

	if rev.Status == types.ReviewStatusApproved || rev.Status == types.ReviewStatusRejected {
		if err := pgsession.New().UpdateStatus(ctx, rev.Session, types.SessionStatusReady); err != nil {
			return nil, fmt.Errorf("save sesession as ready error: %v", err)
		}
		// release the connection if there's a client waiting
		s.TransportService.ReviewStatusChange(rev)
	}
	return rev, nil
}
