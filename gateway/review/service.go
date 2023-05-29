package review

import (
	"errors"
	"time"

	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Service struct {
		Storage          storage
		TransportService transportService
	}

	storage interface {
		Persist(context *user.Context, review *Review) (int64, error)
		FindById(context *user.Context, id string) (*Review, error)
		FindAll(context *user.Context) ([]Review, error)
		FindBySessionID(sessionID string) (*Review, error)
		FindApprovedJitReviews(ctx *user.Context, connID string) (*Review, error)
		PersistSessionAsReady(s *types.Session) (*st.TxResponse, error)
		FindSessionBySessionId(sessionID string) (*types.Session, error)
	}

	transportService interface {
		ReviewStatusChange(sessionID string, status Status, command []byte)
	}

	Review struct {
		Id             string        `json:"id"                      edn:"xt/id"`
		CreatedAt      time.Time     `json:"created_at"              edn:"review/created-at"`
		Type           string        `json:"type"                    edn:"review/type"`
		Session        string        `json:"session"                 edn:"review/session"`
		Input          string        `json:"input"                   edn:"review/input"`
		AccessDuration time.Duration `json:"access_duration"         edn:"review/access-duration"`
		Status         Status        `json:"status"                  edn:"review/status"`
		RevokeAt       *time.Time    `json:"revoke_at"               edn:"review/revoke-at"`
		CreatedBy      Owner         `json:"created_by"              edn:"review/created-by"`
		Connection     Connection    `json:"connection"              edn:"review/connection"`
		ReviewGroups   []Group       `json:"review_groups,omitempty" edn:"review/review-groups"`
	}

	Owner struct {
		Id    string `json:"id,omitempty"   edn:"xt/id"`
		Name  string `json:"name,omitempty" edn:"user/name"`
		Email string `json:"email"          edn:"user/email"`
	}

	Connection struct {
		Id   string `json:"id,omitempty" edn:"xt/id"`
		Name string `json:"name"         edn:"connection/name"`
	}

	Group struct {
		Id         string  `json:"id"          edn:"xt/id"`
		Group      string  `json:"group"       edn:"review-group/group"`
		Status     Status  `json:"status"      edn:"review-group/status"`
		ReviewedBy *Owner  `json:"reviewed_by" edn:"review-group/reviewed-by"`
		ReviewDate *string `json:"review_date" edn:"review-group/review_date"`
	}

	Status string
)

var (
	ErrNotFound    = errors.New("review not found")
	ErrWrongState  = errors.New("review in wrong state")
	ErrNotEligible = errors.New("not eligible for review")
)

const (
	StatusPending    Status = "PENDING"
	StatusApproved   Status = "APPROVED"
	StatusRejected   Status = "REJECTED"
	StatusRevoked    Status = "REVOKED"
	StatusProcessing Status = "PROCESSING"
	StatusExecuted   Status = "EXECUTED"
	StatusUnknown    Status = "UNKNOWN"

	ReviewTypeJit     = "jit"
	ReviewTypeOneTime = "onetime"
)

func (s *Service) FindOne(context *user.Context, id string) (*Review, error) {
	return s.Storage.FindById(context, id)
}

// FindOneTimeReview returns an one time review by session id
func (s *Service) FindBySessionID(sessionID string) (*Review, error) {
	return s.Storage.FindBySessionID(sessionID)
}

func (s *Service) FindAll(context *user.Context) ([]Review, error) {
	return s.Storage.FindAll(context)
}

func (s *Service) Persist(context *user.Context, review *Review) error {
	if review.Id == "" {
		review.Id = uuid.NewString()
	}

	for i, r := range review.ReviewGroups {
		if r.Id == "" {
			review.ReviewGroups[i].Id = uuid.NewString()
		}
	}

	if _, err := s.Storage.Persist(context, review); err != nil {
		return err
	}
	return nil
}

// FindApprovedJitReviews returns jit reviews that are active based on the access duration
func (s *Service) FindApprovedJitReviews(ctx *user.Context, connID string) (*Review, error) {
	return s.Storage.FindApprovedJitReviews(ctx, connID)
}

func (s *Service) Revoke(ctx *user.Context, reviewID string) (*Review, error) {
	rev, err := s.FindOne(ctx, reviewID)
	if err != nil {
		return nil, err
	}
	// non-jit type reviews cannot be revoked
	if rev == nil || rev.Type != ReviewTypeJit {
		return nil, ErrNotFound
	}
	// only approved reviews could be revoked
	if rev.Status != StatusApproved {
		return nil, ErrWrongState
	}
	rev.Status = StatusRevoked
	return rev, s.Persist(ctx, rev)
}

// called by slack plugin and webapp
func (s *Service) Review(context *user.Context, reviewID string, status Status) (*Review, error) {
	rev, err := s.FindOne(context, reviewID)
	if err != nil {
		return nil, err
	}
	if rev == nil {
		return nil, ErrNotFound
	}
	if rev.Status != StatusPending {
		return rev, ErrWrongState
	}
	isEligibleReviewer := false
	for _, r := range rev.ReviewGroups {
		if pb.IsInList(r.Group, context.User.Groups) {
			isEligibleReviewer = true
			break
		}
	}
	if !isEligibleReviewer {
		return nil, ErrNotEligible
	}

	reviewsCount := len(rev.ReviewGroups)
	approvedCount := 0

	if status == StatusRejected {
		rev.Status = status
	}

	for i, r := range rev.ReviewGroups {
		if pb.IsInList(r.Group, context.User.Groups) {
			t := time.Now().String()
			rev.ReviewGroups[i].Status = status
			rev.ReviewGroups[i].ReviewedBy = &Owner{Id: context.User.Id}
			rev.ReviewGroups[i].ReviewDate = &t
		}
		if rev.ReviewGroups[i].Status == StatusApproved {
			approvedCount++
		}
	}

	if reviewsCount == approvedCount {
		rev.RevokeAt = func() *time.Time { t := time.Now().UTC().Add(rev.AccessDuration); return &t }()
		rev.Status = StatusApproved
	}

	if err := s.Persist(context, rev); err != nil {
		return nil, err
	}

	if rev.Status == StatusApproved || rev.Status == StatusRejected {
		currentSession, err := s.Storage.FindSessionBySessionId(rev.Session)
		if err != nil {
			return nil, err
		}
		_, err = s.Storage.PersistSessionAsReady(currentSession)
		if err != nil {
			return nil, err
		}
		// release the connection if there's a client waiting
		s.TransportService.ReviewStatusChange(rev.Session, rev.Status, []byte(rev.Input))
	}
	return rev, nil
}
