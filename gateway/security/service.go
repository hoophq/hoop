package security

import (
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/analytics"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/storagev2/types"
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
		FindAll(context *user.Context) ([]user.User, error)
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
		SlackID  string      `edn:"login/slack-id"`
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
	stateUID := uuid.NewString()
	if _, err := s.Storage.PersistLogin(&login{Id: stateUID, Redirect: redirect}); err != nil {
		return "", err
	}

	if s.Provider.Audience != "" {
		params := []oauth2.AuthCodeOption{
			oauth2.SetAuthURLParam("audience", s.Provider.Audience),
		}
		return s.Provider.AuthCodeURL(stateUID, params...), nil
	}
	return s.Provider.AuthCodeURL(stateUID), nil
}

func (s *Service) Callback(c *gin.Context, state, code string) string {
	log.With("code", code, "state", state).Infof("starting callback")
	login, err := s.Storage.FindLogin(state)
	if err != nil {
		if login != nil {
			log.With("code", code, "state", state).Debugf("Login not found. Skipping...")
			s.loginOutcome(login, outcomeError)
			return login.Redirect + "?error=unexpected_error"
		}
		log.Warnf("login not found, err=%v", err)
		return fmt.Sprintf("%s/callback?error=unexpected_error", s.Provider.ApiURL)
	}

	log.With("id", login.Id, "code", code, "state", state).Debugf("login found")
	token, idToken, err := s.exchangeCodeByToken(code)
	if err != nil {
		log.Errorf("failed exchanging code by token, reason=%v", err)
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=unexpected_error"
	}
	log.With("id", login.Id, "code", code, "state", state, "issuer", idToken.Issuer, "subject", idToken.Subject).
		Infof("token exchanged")

	var idTokenClaims map[string]any
	if err := idToken.Claims(&idTokenClaims); err != nil {
		log.Errorf("failed extracting id token claims, reason=%v", err)
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=unexpected_error"
	}

	debugClaims(idToken.Subject, idTokenClaims)

	sub, err := s.Provider.VerifyAccessToken(token.AccessToken)
	if err != nil {
		log.Warnf("failed verifiying access token, reason=%v", err)
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=unexpected_error"
	}

	ctx, err := s.UserService.FindBySub(sub)
	if err != nil {
		log.Errorf("failed fetching user by sub, reason=%v", err)
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=unexpected_error"
	}

	var isSignup bool
	if ctx.Org == nil || ctx.User == nil {
		log.Infof("starting signup for sub=%v, multitenant=%v, ctxorg=%v, ctxuser=%v",
			sub, user.IsOrgMultiTenant(), ctx.Org, ctx.User)
		isSignup = true
		switch user.IsOrgMultiTenant() {
		case true:
			err = s.signupMultiTenant(ctx, sub, idTokenClaims)
		default:
			err = s.signup(ctx, sub, idTokenClaims)
		}
		log.Infof("signup finished for sub=%v, success=%v", sub, err == nil)
		if err != nil {
			log.Errorf("failed signup %v, err=%v", sub, err)
			s.loginOutcome(login, outcomeError)
			return login.Redirect + "?error=unexpected_error"
		}
		s.Analytics.Identify(ctx.ToAPIContext())
		s.Analytics.Track(
			ctx.ToAPIContext(),
			analytics.EventSignup,
			map[string]any{"user-agent": c.GetHeader("user-agent")},
		)
	}

	if ctx.User.Status != user.StatusActive {
		log.With("sub", sub, "org", ctx.User.Org).Infof("user is not active")
		s.loginOutcome(login, "unauthorized")
		return login.Redirect + "?error=unauthorized"
	}

	if !isSignup {
		// sync groups if the claim pb.CustomClaimGroups exists
		if email, _, mustSync, groups := parseJWTClaims(idTokenClaims); mustSync {
			log.Infof("syncing groups for %v", email)
			ctx.User.Groups = groups
		}
		if login.SlackID != "" {
			ctx.User.SlackID = login.SlackID
		}
		if err := s.UserService.Persist(ctx.User); err != nil {
			log.Errorf("failed saving user to database, reason=%v", err)
			s.loginOutcome(login, outcomeError)
			return login.Redirect + "?error=unexpected_error"
		}
	}

	s.loginOutcome(login, outcomeSuccess)
	s.Analytics.Track(
		ctx.ToAPIContext(),
		analytics.EventLogin,
		map[string]any{"user-agent": c.GetHeader("user-agent")},
	)

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

func (s *Service) signup(ctx *user.Context, sub string, idTokenClaims map[string]any) error {
	org, err := s.UserService.GetOrgByName(pb.DefaultOrgName)
	if err != nil {
		return fmt.Errorf("failed obtaining default org, err=%v", err)
	}
	userList, err := s.UserService.FindAll(&user.Context{Org: org})
	if err != nil {
		return fmt.Errorf("failed listing users, err=%v", err)
	}
	email, profileName, _, groupList := parseJWTClaims(idTokenClaims)
	// first user is admin
	if len(userList) == 0 {
		groupList = append(groupList, []string{
			types.GroupAdmin,
			types.GroupSecurity,
			types.GroupSRE,
			types.GroupDBA,
			types.GroupDevops,
			types.GroupSupport,
			types.GroupEngineering,
		}...)
	}

	var slackID string
	if iuser, _ := s.UserService.FindInvitedUser(email); iuser != nil {
		slackID = iuser.SlackID
	}
	ctx.User = &user.User{
		Id:      sub,
		Org:     org.Id,
		Name:    profileName,
		Email:   email,
		Status:  user.StatusActive,
		SlackID: slackID,
		Groups:  groupList,
	}
	ctx.Org = org

	if err := s.UserService.Persist(ctx.User); err != nil {
		return fmt.Errorf("failed persisting user %v to default org, err=%v", sub, err)
	}
	return nil
}

func (s *Service) signupMultiTenant(context *user.Context, sub string, idTokenClaims map[string]any) error {
	email, profileName, _, groups := parseJWTClaims(idTokenClaims)
	newOrg := false

	invitedUser, err := s.UserService.FindInvitedUser(email)
	if err != nil {
		return err
	}

	if context.Org == nil && invitedUser == nil {
		orgName, _ := idTokenClaims[pb.CustomClaimOrg].(string)
		if orgName == "" {
			orgName = user.ExtractDomain(email)
		}
		orgData, err := s.UserService.GetOrgByName(orgName)
		if err != nil {
			return err
		}

		if orgData == nil {
			orgData = &user.Org{
				Id:   uuid.NewString(),
				Name: orgName,
			}

			if err := s.UserService.Persist(orgData); err != nil {
				return err
			}

			newOrg = true
		}

		context.Org = orgData
	}

	if context.User == nil {
		status := user.StatusReviewing
		if s.Provider.Issuer != idp.DefaultProviderIssuer {
			status = user.StatusActive
		}

		if newOrg {
			status = user.StatusActive
			if len(groups) == 0 {
				groups = append(groups,
					types.GroupAdmin,
					types.GroupSecurity,
					types.GroupSRE,
					types.GroupDBA,
					types.GroupDevops,
					types.GroupSupport,
					types.GroupEngineering)
			}
		}

		var slackID string
		if invitedUser != nil {
			slackID = invitedUser.SlackID
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
			if len(groups) == 0 {
				groups = invitedUser.Groups
			}
		}

		context.User = &user.User{
			Id:      sub,
			Org:     context.Org.Id,
			Name:    profileName,
			Email:   email,
			Status:  status,
			SlackID: slackID,
			Groups:  groups,
		}

		if err := s.UserService.Persist(context.User); err != nil {
			return err
		}
	}

	return nil
}

func parseJWTClaims(idTokenClaims map[string]any) (email, profile string, syncGroups bool, groups []string) {
	email, _ = idTokenClaims["email"].(string)
	profile, _ = idTokenClaims["name"].(string)
	switch groupsClaim := idTokenClaims[pb.CustomClaimGroups].(type) {
	case string:
		syncGroups = true
		if groupsClaim != "" {
			groups = []string{groupsClaim}
		}
	case []any:
		syncGroups = true
		for _, g := range groupsClaim {
			groupName, _ := g.(string)
			if groupName == "" {
				continue
			}
			groups = append(groups, groupName)
		}
	case nil: // noop
	default:
		log.Errorf("failed syncing group claims, reason=unknown type:%T", groupsClaim)
	}
	return
}

func debugClaims(subject string, claims map[string]any) {
	log := log.With()
	for claimKey, claimVal := range claims {
		if claimKey == pb.CustomClaimGroups {
			log = log.With(claimKey, fmt.Sprintf("%q", claimVal))
			continue
		}
		log = log.With(claimKey, fmt.Sprintf("%v", claimVal))
	}
	log.Infof("id_token claims=%v, subject=%s, admingroup=%q", len(claims), subject, types.GroupAdmin)
}

func (s *Service) loginOutcome(login *login, outcome outcomeType) {
	login.Outcome = outcome
	s.Storage.PersistLogin(login)
}
