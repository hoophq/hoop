package user

import (
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/storagev2"
	serviceaccountstorage "github.com/runopsio/hoop/gateway/storagev2/serviceaccount"
	"github.com/runopsio/hoop/gateway/storagev2/types"
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
		GetOrgNameByID(orgID string) (*Org, error)
		FindByGroups(context *Context, groups []string) ([]User, error)
		ListAllGroups(context *Context) ([]string, error)
		FindOrgs() ([]Org, error)
	}

	Context struct {
		Org  *Org
		User *User
	}

	Org struct {
		Id      string `json:"id"     edn:"xt/id"`
		Name    string `json:"name"   edn:"org/name" binding:"required"`
		IsApiV2 bool   `json:"api_v2" edn:"org/api-v2"`
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

	ContextLoggerKey = "context-logger"
	ContextUserKey   = "context"
)

var statuses = []StatusType{
	StatusActive,
	StatusReviewing,
	StatusInactive,
}

// ToAPIContext converts a *user.Context to the new structure *types.APIContext
func (c *Context) ToAPIContext() *types.APIContext {
	if c == nil || c.User == nil {
		// avoid panic if the context is not set for some reason
		return &types.APIContext{}
	}
	apiCtx := &types.APIContext{
		UserID:     c.User.Id,
		UserName:   c.User.Name,
		UserEmail:  c.User.Email,
		UserGroups: c.User.Groups,
		UserStatus: string(c.User.Status),
		SlackID:    c.User.SlackID,
	}
	if c.Org != nil {
		apiCtx.OrgID = c.Org.Id
		apiCtx.OrgName = c.Org.Name
		apiCtx.IsApiV2 = c.Org.IsApiV2
	}
	return apiCtx
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

func (s *Service) GetOrgNameByID(id string) (*Org, error) {
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
	// if this env is not set, it will by default
	// create the organization to proxy requests to the new api.
	isLegacyApi := os.Getenv("LEGACY_API") == "true"
	switch len(orgList) {
	case 1:
		org := &orgList[0]
		if org.Name == pb.DefaultOrgName {
			return nil
		}
		org.IsApiV2 = !isLegacyApi
		org.Name = pb.DefaultOrgName
		if err := s.Persist(org); err != nil {
			return fmt.Errorf("failed promoting %v to single tenant, err=%v", orgList[0], err)
		}
	case 0:
		if err := s.Persist(&Org{
			Id:      uuid.NewString(),
			Name:    pb.DefaultOrgName,
			IsApiV2: !isLegacyApi,
		}); err != nil {
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
	return pb.IsInList(types.GroupAdmin, user.Groups)
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

// GetUserContext loads a user or a service account type
func GetUserContext(usrSvc service, subject string) (*Context, error) {
	userCtx, err := usrSvc.FindBySub(subject)
	if err != nil {
		return nil, fmt.Errorf("failed obtaining user from store: %v", err)
	}
	if userCtx.User == nil {
		ctx := storagev2.NewContext("", "", storagev2.NewStorage(nil))
		objID := serviceaccountstorage.DeterministicXtID(subject)
		sa, err := serviceaccountstorage.GetEntity(ctx, objID)
		if err != nil {
			return nil, fmt.Errorf("failed obtaining service account from store: %v", err)
		}
		if sa == nil {
			return &Context{}, nil
		}
		userCtx = NewContext(sa.OrgID, sa.ID)
		userCtx.User.Groups = sa.Groups
		userCtx.User.Email = sa.Subject
		userCtx.User.Name = sa.Name
		return userCtx, nil
	}
	return userCtx, nil
}
