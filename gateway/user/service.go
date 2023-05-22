package user

import (
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
)

type (
	Service struct {
		Storage storage
	}

	storage interface {
		Signup(org *Org, user *User) (txId int64, err error)
		FindById(identifier string) (*Context, error)
		FindByEmail(ctx *Context, email string) (*User, error)
		FindBySlackID(ctx *Org, slackID string) (*User, error)
		Persist(user any) (int64, error)
		FindAll(context *Context) ([]User, error)
		FindInvitedUser(email string) (*InvitedUser, error)
		GetOrgByName(name string) (*Org, error)
		GetOrgNameByID(orgID string) (string, error)
		FindByGroups(context *Context, groups []string) ([]User, error)
		ListAllGroups(context *Context) ([]string, error)
		FindOrgs() ([]Org, error)
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
		Id      string     `json:"id"       edn:"xt/id"`
		Org     string     `json:"-"        edn:"user/org"`
		Name    string     `json:"name"     edn:"user/name"`
		Email   string     `json:"email"    edn:"user/email"`
		Status  StatusType `json:"status"   edn:"user/status"`
		SlackID string     `json:"slack_id" edn:"user/slack-id"`
		Groups  []string   `json:"groups"   edn:"user/groups"`
	}

	InvitedUser struct {
		Id      string   `json:"id"       edn:"xt/id"`
		Org     string   `json:"-"        edn:"invited-user/org"`
		Email   string   `json:"email"    edn:"invited-user/email"`
		Name    string   `json:"name"     end:"invited-user/name"`
		SlackID string   `json:"slack_id" edn:"invited-user/slack-id"`
		Groups  []string `json:"groups"   edn:"invited-user/groups"`
	}

	StatusType string
)

const (
	StatusActive    StatusType = "active"
	StatusReviewing StatusType = "reviewing"
	StatusInactive  StatusType = "inactive"

	GroupAdmin       string = "admin"
	GroupSecurity    string = "security"
	GroupSRE         string = "sre"
	GroupDBA         string = "dba"
	GroupDevops      string = "devops"
	GroupSupport     string = "support"
	GroupEngineering string = "engineering"

	ContextLoggerKey = "context-logger"
	ContextUserKey   = "context"
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

func (s *Service) FindByEmail(ctx *Context, email string) (*User, error) {
	return s.Storage.FindByEmail(ctx, email)
}

func (s *Service) FindBySlackID(ctx *Org, slackID string) (*User, error) {
	return s.Storage.FindBySlackID(ctx, slackID)
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

func (s *Service) GetOrgNameByID(id string) (string, error) {
	return s.Storage.GetOrgNameByID(id)
}

func (s *Service) FindInvitedUser(email string) (*InvitedUser, error) {
	return s.Storage.FindInvitedUser(email)
}

func (s *Service) FindByGroups(context *Context, groups []string) ([]User, error) {
	return s.Storage.FindByGroups(context, groups)
}

func (s *Service) ListAllGroups(context *Context) ([]string, error) {
	return s.Storage.ListAllGroups(context)
}

func (s *Service) FindOrgs() ([]Org, error) {
	return s.Storage.FindOrgs()
}

// CreateDefaultOrganization creates a default organization if there isn't any.
// In case of a single existing organization, try to promote it (change the name of org).
// Having multiple organizations returns an error and a manual intervention is
// necessary to remove additional organizations.
func (s *Service) CreateDefaultOrganization() error {
	orgList, err := s.FindOrgs()
	if err != nil {
		return err
	}
	switch len(orgList) {
	case 1:
		org := &orgList[0]
		if org.Name == pb.DefaultOrgName {
			return nil
		}
		org.Name = pb.DefaultOrgName
		if err := s.Persist(org); err != nil {
			return fmt.Errorf("failed promoting %v to single tenant, err=%v", orgList[0], err)
		}
	case 0:
		if err := s.Persist(&Org{Id: uuid.NewString(), Name: pb.DefaultOrgName}); err != nil {
			return fmt.Errorf("failed creating the default organization, err=%v", err)
		}
	default:
		return fmt.Errorf("found multiple organizations, cannot promote. orgs=%v", orgList)
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

func IsOrgMultiTenant() bool {
	return os.Getenv("ORG_MULTI_TENANT") == "true"
}

// NewContext returns a user.Context with Id and Org ID set
func NewContext(orgID, userID string) *Context {
	return &Context{Org: &Org{Id: orgID}, User: &User{Id: userID}}
}
