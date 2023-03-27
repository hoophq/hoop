package security

import (
	"fmt"
	"log"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/user"
	"golang.org/x/oauth2"
)

type (
	Service struct {
		Storage     storage
		UserService UserService
		Provider    *idp.Provider
		Analytics   user.Analytics
	}

	storage interface {
		PersistLogin(login *login) (int64, error)
		FindLogin(state string) (*login, error)
	}

	UserService interface {
		FindBySub(sub string) (*user.Context, error)
		GetOrgByName(name string) (*user.Org, error)
		GetOrgNameByID(id string) (string, error)
		FindInvitedUser(email string) (*user.InvitedUser, error)
		Persist(u any) error
	}

	login struct {
		Id       string      `edn:"xt/id"`
		Redirect string      `edn:"login/redirect"`
		Outcome  outcomeType `edn:"login/outcome"`
	}

	outcomeType string
)

const (
	outcomeSuccess       outcomeType = "success"
	outcomeError         outcomeType = "error"
	pendingReviewError   outcomeType = "pending_review"
	outcomeEmailMismatch outcomeType = "email_mismatch"
)

var errAuthDisabled = fmt.Errorf("authentication is disabled when running on dev mode")

func (s *Service) Login(redirect string) (string, error) {
	if s.Provider.Profile == pb.DevProfile {
		return "", errAuthDisabled
	}
	login := &login{
		Id:       uuid.NewString(),
		Redirect: redirect,
	}

	_, err := s.Storage.PersistLogin(login)
	if err != nil {
		return "", err
	}

	if s.Provider.Audience != "" {
		params := []oauth2.AuthCodeOption{
			oauth2.SetAuthURLParam("audience", s.Provider.Audience),
		}
		return s.Provider.AuthCodeURL(login.Id, params...), nil
	}

	return s.Provider.AuthCodeURL(login.Id), nil
}

func (s *Service) Callback(state, code string) string {
	log.With("code", code, "state", state).Debugf("starting callback")
	login, err := s.Storage.FindLogin(state)
	if err != nil {
		if login != nil {
			log.With("code", code, "state", state).Debugf("Login not found. Skipping...")
			s.loginOutcome(login, outcomeError)
			return login.Redirect + "?error=unexpected_error"
		}
		return "https://app.hoop.dev/callback?error=unexpected_error"
	}

	log.With("code", code, "state", state).Debugf("Found login: %v", login)
	token, idToken, err := s.exchangeCodeByToken(code)
	if err != nil {
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=unexpected_error"
	}

	var idTokenClaims map[string]any
	if err := idToken.Claims(&idTokenClaims); err != nil {
		s.loginOutcome(login, outcomeError)
		log.Errorf("failed extracting ID Token claims, err: %v\n", err)
		return login.Redirect + "?error=unexpected_error"
	}

	sub, err := s.Provider.VerifyAccessToken(token.AccessToken)
	if err != nil {
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=unexpected_error"
	}

	context, err := s.UserService.FindBySub(sub)
	if err != nil {
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=unexpected_error"
	}

	if context.Org == nil || context.User == nil {
		if err := s.signup(context, sub, idTokenClaims); err != nil {
			s.loginOutcome(login, outcomeError)
			return login.Redirect + "?error=unexpected_error"
		}
	}

	if context.User.Status != user.StatusActive {
		s.loginOutcome(login, pendingReviewError)
		return login.Redirect + "?error=pending_review"
	}

	groupsClaim, _ := idTokenClaims[pb.CustomClaimGroups].([]any)
	if len(groupsClaim) > 0 {
		groups := mapGroupsToString(groupsClaim)

		context.User.Groups = groups
		if err := s.UserService.Persist(context.User); err != nil {
			s.loginOutcome(login, outcomeError)
			return login.Redirect + "?error=unexpected_error"
		}
	}

	s.loginOutcome(login, outcomeSuccess)
	s.Analytics.Track(context.User.Id, "login", map[string]any{})

	return login.Redirect + "?token=" + token.AccessToken
}

func (s *Service) exchangeCodeByToken(code string) (*oauth2.Token, *oidc.IDToken, error) {
	log.With("code", code).Debugf("verifying access token")
	token, err := s.Provider.Exchange(s.Provider.Context, code)
	if err != nil {
		log.Errorf("failed to exchange authorization code, err: %v\n", err)
		return nil, nil, err
	}

	idToken, err := s.Provider.VerifyIDToken(token)
	if err != nil {
		log.Errorf("failed to verify ID Token, err: %v\n", err)
		return nil, nil, err
	}

	return token, idToken, nil
}

func (s *Service) signup(context *user.Context, sub string, idTokenClaims map[string]any) error {
	email, _ := idTokenClaims["email"].(string)
	profileName, _ := idTokenClaims["name"].(string)
	newOrg := false

	invitedUser, err := s.UserService.FindInvitedUser(email)
	if err != nil {
		return err
	}

	if context.Org == nil && invitedUser == nil {
		org, ok := idTokenClaims[pb.CustomClaimOrg].(string)
		if !ok || org == "" {
			org = user.ExtractDomain(email)
		}

		orgData, err := s.UserService.GetOrgByName(org)
		if err != nil {
			return err
		}

		if orgData == nil {
			orgData = &user.Org{
				Id:   uuid.NewString(),
				Name: org,
			}

			if err := s.UserService.Persist(orgData); err != nil {
				return err
			}

			newOrg = true
		}

		context.Org = orgData
	}

	if context.User == nil {
		groups := make([]string, 0)
		groupsClaim, _ := idTokenClaims[pb.CustomClaimGroups].([]any)
		if len(groupsClaim) > 0 {
			groups = mapGroupsToString(groupsClaim)
		}
		status := user.StatusReviewing
		if s.Provider.Issuer != idp.DefaultProviderIssuer {
			status = user.StatusActive
		}

		if newOrg {
			status = user.StatusActive
			if len(groupsClaim) == 0 {
				groups = append(groups,
					user.GroupAdmin,
					user.GroupSecurity,
					user.GroupSRE,
					user.GroupDBA,
					user.GroupDevops,
					user.GroupSupport,
					user.GroupEngineering)
			}
		}

		if invitedUser != nil {
			if context.Org == nil {
				orgName, err := s.UserService.GetOrgNameByID(invitedUser.Org)
				if err != nil {
					return err
				}
				context.Org = &user.Org{
					Id:   invitedUser.Org,
					Name: orgName,
				}
			}

			status = user.StatusActive
			if len(groupsClaim) == 0 {
				groups = invitedUser.Groups
			}
		}

		context.User = &user.User{
			Id:     sub,
			Org:    context.Org.Id,
			Name:   profileName,
			Email:  email,
			Status: status,
			Groups: groups,
		}

		if err := s.UserService.Persist(context.User); err != nil {
			return err
		}

		s.Analytics.Identify(context)
		s.Analytics.Track(context.User.Id, "signup", map[string]any{})
	}

	return nil
}

func (s *Service) loginOutcome(login *login, outcome outcomeType) {
	login.Outcome = outcome
	s.Storage.PersistLogin(login)
}

func mapGroupsToString(groupsClaim []any) []string {
	groups := make([]string, 0)
	for _, g := range groupsClaim {
		groupName, _ := g.(string)
		if groupName == "" {
			continue
		}
		groups = append(groups, groupName)
	}
	return groups
}
