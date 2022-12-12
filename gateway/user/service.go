package user

import (
	pb "github.com/runopsio/hoop/common/proto"
	"strings"
)

type (
	Service struct {
		Storage storage
	}

	storage interface {
		Signup(org *Org, user *User) (txId int64, err error)
		FindById(identifier string) (*Context, error)
		Persist(user any) (int64, error)
		FindAll(context *Context) ([]User, error)
		FindInvitedUser(email string) (*InvitedUser, error)
		GetOrgByName(name string) (*Org, error)
	}

	Context struct {
		Org  *Org
		User *User
	}

	Org struct {
		Id   string `json:"id"   edn:"xt/id"`
		Name string `json:"name" edn:"org/name" binding:"required"`
	}

	User struct {
		Id     string     `json:"id"     edn:"xt/id"`
		Org    string     `json:"-"      edn:"user/org"`
		Name   string     `json:"name"   edn:"user/name"`
		Email  string     `json:"email"  edn:"user/email"`
		Status StatusType `json:"status" edn:"user/status"`
		Groups []string   `json:"groups" edn:"user/groups"`
	}

	InvitedUser struct {
		Id     string   `json:"id"     edn:"xt/id"`
		Org    string   `json:"-"      edn:"invited-user/org"`
		Email  string   `json:"email"  edn:"invited-user/email"`
		Name   string   `json:"name"   end:"invited-user/name`
		Groups []string `json:"groups" edn:"invited-user/groups"`
	}

	StatusType string
)

const (
	StatusActive    StatusType = "active"
	StatusReviewing StatusType = "reviewing"
	StatusInactive  StatusType = "inactive"

	GroupAdmin string = "admin"
)

var statuses = []StatusType{
	StatusActive,
	StatusReviewing,
	StatusInactive,
}

func (s *Service) FindAll(context *Context) ([]User, error) {
	return s.Storage.FindAll(context)
}

func (s *Service) FindOne(context *Context, id string) (*User, error) {
	ctx, err := s.Storage.FindById(id)
	if err != nil {
		return nil, err
	}

	if ctx.User == nil || ctx.User.Org != context.Org.Id {
		return nil, nil
	}

	return ctx.User, nil

}

func (s *Service) FindBySub(sub string) (*Context, error) {
	return s.Storage.FindById(sub)
}

func (s *Service) Persist(user any) error {
	_, err := s.Storage.Persist(user)
	if err != nil {
		return err
	}
	return nil
}

func (s *Service) Signup(org *Org, user *User) (txId int64, err error) {
	return s.Storage.Signup(org, user)
}

func (s *Service) GetOrgByName(name string) (*Org, error) {
	return s.Storage.GetOrgByName(name)
}

func (s *Service) FindInvitedUser(email string) (*InvitedUser, error) {
	return s.Storage.FindInvitedUser(email)
}

func ExtractDomain(email string) string {
	emailsParts := strings.Split(email, "@")
	domainParts := strings.Split(emailsParts[1], ".")
	orgName := domainParts[0]

	if isPublicDomain(orgName) {
		orgName = emailsParts[0]
	}

	return orgName
}

func isPublicDomain(domain string) bool {
	publicDomains := []string{
		"gmail",
		"outlook",
		"hotmail",
		"yahoo",
		"protonmail",
		"zoho",
		"aim",
		"gmx",
		"icloud",
		"yandex",
	}

	for _, d := range publicDomains {
		if domain == d {
			return true
		}
	}
	return false
}

func (user *User) IsAdmin() bool {
	return pb.IsInList(GroupAdmin, user.Groups)
}

func isInStatus(status StatusType) bool {
	for _, s := range statuses {
		if s == status {
			return true
		}
	}
	return false
}
