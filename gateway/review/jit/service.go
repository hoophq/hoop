package jit

import (
	"errors"
	"time"

	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Service struct {
		Storage          storage
		TransportService transportService
	}

	storage interface {
		Persist(context *user.Context, jit *Jit) (int64, error)
		FindById(context *user.Context, id string) (*Jit, error)
		FindAll(context *user.Context) ([]Jit, error)
		FindBySessionID(sessionID string) (*Jit, error)
	}

	transportService interface {
		JitStatusChange(sessionID string, status Status)
	}

	Jit struct {
		Id         string        `json:"id"                      edn:"xt/id"`
		Session    string        `json:"session"                 edn:"jit/session"`
		Time       time.Duration `json:"time"                    edn:"jit/time"`
		Status     Status        `json:"status"                  edn:"jit/status"`
		CreatedBy  Owner         `json:"created_by"              edn:"jit/created-by"`
		Connection Connection    `json:"connection"              edn:"jit/connection"`
		JitGroups  []Group       `json:"jit_groups,omitempty"    edn:"jit/jit-groups"`
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
		Group      string  `json:"group"       edn:"jit-group/group"`
		Status     Status  `json:"status"      edn:"jit-group/status"`
		ReviewedBy *Owner  `json:"reviewed_by" edn:"jit-group/reviewed-by"`
		ReviewDate *string `json:"review_date" edn:"jit-group/review-date"`
	}

	Status string
)

const (
	StatusPending  Status = "PENDING"
	StatusApproved Status = "APPROVED"
	StatusRejected Status = "REJECTED"
)

var (
	ErrNotFound    = errors.New("jit review not found")
	ErrWrongState  = errors.New("jit review in wrong state")
	ErrNotEligible = errors.New("not eligible for jit review")
)

func (s *Service) FindOne(context *user.Context, id string) (*Jit, error) {
	return s.Storage.FindById(context, id)
}

func (s *Service) FindBySessionID(sessionID string) (*Jit, error) {
	return s.Storage.FindBySessionID(sessionID)
}

func (s *Service) FindAll(context *user.Context) ([]Jit, error) {
	return s.Storage.FindAll(context)
}

func (s *Service) Persist(context *user.Context, jit *Jit) error {
	if jit.Id == "" {
		jit.Id = uuid.NewString()
	}

	for i, r := range jit.JitGroups {
		if r.Id == "" {
			jit.JitGroups[i].Id = uuid.NewString()
		}
	}

	if _, err := s.Storage.Persist(context, jit); err != nil {
		return err
	}
	return nil
}

func (s *Service) Review(context *user.Context, jitID string, status Status) (*Jit, error) {
	j, err := s.FindOne(context, jitID)
	if err != nil {
		return nil, err
	}

	if j == nil {
		return nil, ErrNotFound
	}

	if j.Status != StatusPending {
		return j, ErrWrongState
	}

	if !pb.IsInList(user.GroupAdmin, context.User.Groups) {
		return nil, ErrNotEligible
	}

	jitsCount := len(j.JitGroups)
	approvedCount := 0

	if status == StatusRejected {
		j.Status = status
	}

	for i, r := range j.JitGroups {
		if pb.IsInList(r.Group, context.User.Groups) {
			t := time.Now().String()
			j.JitGroups[i].Status = status
			j.JitGroups[i].ReviewedBy = &Owner{Id: context.User.Id}
			j.JitGroups[i].ReviewDate = &t
		}
		if j.JitGroups[i].Status == StatusApproved {
			approvedCount++
		}
	}

	if jitsCount == approvedCount && status != StatusRejected {
		j.Status = StatusApproved
	}

	if err := s.Persist(context, j); err != nil {
		return nil, err
	}

	if j.Status == StatusApproved || j.Status == StatusRejected {
		s.TransportService.JitStatusChange(j.Session, j.Status)
	}

	return j, nil
}
