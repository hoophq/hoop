package user

import "strings"

type (
	Service struct {
		Storage storage
	}

	storage interface {
		Signup(org *Org, user *User) (txId int64, err error)
		FindById(email string) (*Context, error)
		GetOrgByName(name string) (*Org, error)
		Persist(user any) (int64, error)
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
		Email  string     `json:"email"  edn:"user/email" binding:"required"`
		Status StatusType `json:"status" edn:"user/status"`
		Groups []string   `json:"groups" edn:"user/groups"`
	}

	StatusType string
)

const (
	StatusActive    StatusType = "active"
	StatusReviewing StatusType = "reviewing"

	GroupAdmin string = "admin"
)

func (s *Service) Signup(org *Org, user *User) (txId int64, err error) {
	return s.Storage.Signup(org, user)
}

func (s *Service) FindBySub(sub string) (*Context, error) {
	context, err := s.Storage.FindById(sub)
	if err != nil {
		return nil, err
	}

	if context.User == nil {
		return context, nil
	}

	orgName := ExtractDomain(context.User.Email)

	if context.Org == nil {
		org, err := s.Storage.GetOrgByName(orgName)
		if err != nil {
			return nil, err
		}
		context.Org = org
	}

	return context, nil
}

func (s *Service) Persist(user any) error {
	_, err := s.Storage.Persist(user)
	if err != nil {
		return err
	}
	return nil
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
