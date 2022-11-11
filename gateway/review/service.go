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
		Id           string        `json:"id"            edn:"xt/id"`
		Session      string        `json:"session"       edn:"review/session"`
		Command      string        `json:"command"       edn:"review/command"`
		Status       Status        `json:"status"        edn:"review/status"`
		CreatedBy    Owner         `json:"created_by"    edn:"review/created-by"`
		Connection   Connection    `json:"connection"    edn:"review/connection"`
		ReviewGroups []ReviewGroup `json:"review_groups" edn:"review/review-groups"`
	}

	Owner struct {
		Id    string `json:"id"    edn:"xt/id"`
		Name  string `json:"name"  edn:"user/name"`
		Email string `json:"email" edn:"user/email"`
	}

	Connection struct {
		Id   string `json:"id"   edn:"xt/id"`
		Name string `json:"name" edn:"connection/name"`
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
	return nil
}
