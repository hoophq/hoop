package review

import (
	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/user"
	"time"
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
	}

	transportService interface {
		ReviewStatusChange(sessionID string, status Status, command []byte) error
	}

	Review struct {
		Id           string     `json:"id"                      edn:"xt/id"`
		Session      string     `json:"session"                 edn:"review/session"`
		Input        string     `json:"input"                   edn:"review/input"`
		Status       Status     `json:"status"                  edn:"review/status"`
		CreatedBy    Owner      `json:"created_by"              edn:"review/created-by"`
		Connection   Connection `json:"connection"              edn:"review/connection"`
		ReviewGroups []Group    `json:"review_groups,omitempty" edn:"review/review-groups"`
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

const (
	StatusPending    Status = "PENDING"
	StatusApproved   Status = "APPROVED"
	StatusRejected   Status = "REJECTED"
	StatusProcessing Status = "PROCESSING"
	StatusExecuted   Status = "EXECUTED"
	StatusUnknown    Status = "UNKNOWN"
)

func (s *Service) FindOne(context *user.Context, id string) (*Review, error) {
	return s.Storage.FindById(context, id)
}

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

func (s *Service) Review(context *user.Context, existingReview *Review, status Status) error {
	reviewsCount := len(existingReview.ReviewGroups)
	approvedCount := 0

	if status == StatusRejected {
		existingReview.Status = status
	}

	for i, r := range existingReview.ReviewGroups {
		if pb.IsInList(r.Group, context.User.Groups) {
			t := time.Now().String()
			existingReview.ReviewGroups[i].Status = status
			existingReview.ReviewGroups[i].ReviewedBy = &Owner{Id: context.User.Id}
			existingReview.ReviewGroups[i].ReviewDate = &t
		}
		if existingReview.ReviewGroups[i].Status == StatusApproved {
			approvedCount++
		}
	}

	if reviewsCount == approvedCount {
		existingReview.Status = StatusApproved
	}

	if err := s.Persist(context, existingReview); err != nil {
		return err
	}

	if existingReview.Status == StatusApproved || existingReview.Status == StatusRejected {
		if err := s.TransportService.ReviewStatusChange(existingReview.Session, existingReview.Status, []byte(existingReview.Input)); err != nil {
			return err
		}
	}
	return nil
}
