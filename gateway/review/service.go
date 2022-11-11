package review

import (
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Service struct {
		Storage storage
	}

	storage interface {
		Persist(context *user.Context, review *Review) (int64, error)
		FindById(context *user.Context, id string) (*Review, error)
		FindAll(context *user.Context) ([]Review, error)
	}

	Review struct {
		Id           string        `json:"id"`
		OrgId        string        `json:"-"`
		CreatedById  Owner         `json:"created_by"`
		Command      string        `json:"command"`
		Status       Status        `json:"status"`
		ReviewGroups []ReviewGroup `json:"review_groups"`
	}

	Owner struct {
		Id    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	ReviewGroup struct {
		Id         string `json:"id"          edn:"xt/id"`
		Group      string `json:"group"       edn:"review-group/group"`
		Status     Status `json:"status"      edn:"review-group/status"`
		ReviewedBy string `json:"reviewed_by" edn:"review-group/reviewed-by"`
		ReviewDate string `json:"review_date" edn:"review-group/review_date"`
	}

	Status string
)

const (
	StatusPending  Status = "PENDING"
	StatusApproved Status = "APPROVED"
	StatusRejected Status = "REJECTED"
)

func (s *Service) FindOne(context *user.Context, id string) (*Review, error) {
	return s.Storage.FindById(context, id)
}

func (s *Service) FindAll(context *user.Context) ([]Review, error) {
	return nil, nil
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
	return nil
}
