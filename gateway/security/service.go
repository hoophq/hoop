package security

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/analytics"
	"github.com/runopsio/hoop/gateway/pgrest"
	pguserauth "github.com/runopsio/hoop/gateway/pgrest/userauth"
	pgusers "github.com/runopsio/hoop/gateway/pgrest/users"
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
		GetOrgByName(name string) (*user.Org, error)
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
	outcomeUnauthorized  outcomeType = "unauthorized"
)

var errAuthDisabled = fmt.Errorf("authentication is disabled when running on dev mode")

func (s *Service) Login(redirect string) (string, error) {
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
		log.Warnf("failed obtaining login, err=%v", err)
		return fmt.Sprintf("%s/callback?error=unexpected_error", s.Provider.ApiURL)
	}
	if login == nil {
		log.With("code", code, "state", state).Infof("login not found")
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

	debugClaims(idToken.Subject, idTokenClaims, token)

	sub, err := s.Provider.VerifyAccessToken(token.AccessToken)
	if err != nil {
		log.Warnf("failed verifiying access token, reason=%v", err)
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=unexpected_error"
	}

	authUserCtx, err := pguserauth.New().FetchUserContext(sub)
	if err != nil {
		log.Errorf("failed fetching user by sub, reason=%v", err)
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=unexpected_error"
	}
	ctx := toLegacyUserContext(authUserCtx)

	var isSignup bool
	if authUserCtx.IsEmpty() {
		log.Infof("starting signup for sub=%v, multitenant=%v, ctxorg=%v, ctxuser=%v",
			sub, user.IsOrgMultiTenant(), ctx.Org, ctx.User)
		isSignup = true
		switch user.IsOrgMultiTenant() {
		case true:
			// TODO: this function is not being used because we removed the signup
			// capabilities and pinned the deployment (1.18.33) of new multi tenant instances.
			// This code needs review after signup is re-enabled.
			// err = s.signupMultiTenant(ctx, sub, idTokenClaims)
			err = fmt.Errorf("signup is disabled for multi tenant instances")
		default:
			err = s.signup(ctx, sub, idTokenClaims)
		}
		log.Infof("signup finished for sub=%v, success=%v", sub, err == nil)
		if err != nil {
			log.Warnf("failed signup %v, err=%v", sub, err)
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
		s.loginOutcome(login, outcomeUnauthorized)
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
		err = pgusers.New().Upsert(pgrest.User{
			ID:       authUserCtx.UserUUID,
			OrgID:    ctx.User.Org,
			Subject:  ctx.User.Id,
			Name:     ctx.User.Name,
			Email:    ctx.User.Email,
			Verified: true, // an authenticated user is always verified
			Status:   string(ctx.User.Status),
			SlackID:  ctx.User.SlackID,
			Groups:   ctx.User.Groups,
		})
		if err != nil {
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

	ctx.User = &user.User{
		Id:      sub,
		Org:     org.Id,
		Name:    profileName,
		Email:   email,
		Status:  user.StatusActive,
		SlackID: "",
		Groups:  groupList,
	}
	ctx.Org = org
	iuser, err := pgusers.New().FetchUnverifiedUser(org, email)
	if err != nil {
		return fmt.Errorf("failed fetching user %s, err=%v", email, err)
	}
	if iuser != nil {
		if iuser.Status != string(user.StatusActive) {
			return fmt.Errorf("user %s/%s is not active", sub, iuser.Email)
		}
		iuser.Subject = ctx.User.Id
		iuser.Status = string(ctx.User.Status)
		iuser.Verified = true
		if len(ctx.User.Name) > 0 {
			iuser.Name = ctx.User.Name
		}
		if len(ctx.User.Groups) > 0 {
			iuser.Groups = ctx.User.Groups
		}
		if err := pgusers.New().Upsert(*iuser); err != nil {
			return fmt.Errorf("failed updating unverified user %s/%s, err=%v", sub, iuser.Email, err)
		}
		return nil
	}
	err = pgusers.New().Upsert(pgrest.User{
		ID:       uuid.NewString(),
		OrgID:    ctx.Org.Id,
		Subject:  ctx.User.Id,
		Name:     ctx.User.Name,
		Email:    ctx.User.Email,
		Status:   string(ctx.User.Status),
		Verified: true,
		SlackID:  "",
		Groups:   ctx.User.Groups,
	})
	if err != nil {
		return fmt.Errorf("failed persisting user %v to default org, err=%v", sub, err)
	}
	return nil
}

// func (s *Service) signupMultiTenant(context *user.Context, sub string, idTokenClaims map[string]any) error {
// 	email, profileName, _, groups := parseJWTClaims(idTokenClaims)
// 	invitedUser, err := s.UserService.FindInvitedUser(email)
// 	if err != nil {
// 		return err
// 	}

// 	if context.Org == nil && invitedUser == nil {
// 		return fmt.Errorf("user %s was not invited", email)
// 	}

// 	if context.User == nil && invitedUser != nil {
// 		if context.Org == nil {
// 			org, err := s.UserService.GetOrgNameByID(invitedUser.Org)
// 			if err != nil {
// 				return err
// 			}
// 			if org == nil {
// 				return fmt.Errorf("failed to obtain organization %q", invitedUser.Org)
// 			}
// 			context.Org = &user.Org{
// 				Id:   invitedUser.Org,
// 				Name: org.Name,
// 			}
// 		}

// 		// add groups from invited user if none were found in the jwt claims
// 		if len(groups) == 0 {
// 			groups = invitedUser.Groups
// 		}
// 		context.User = &user.User{
// 			Id:      sub,
// 			Org:     context.Org.Id,
// 			Name:    profileName,
// 			Email:   email,
// 			Status:  user.StatusActive,
// 			SlackID: invitedUser.SlackID,
// 			Groups:  groups,
// 		}
// 		return s.UserService.Persist(context.User)
// 	}

// 	return nil
// }

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

func toLegacyUserContext(ctx *pguserauth.Context) *user.Context {
	if ctx.IsEmpty() {
		return &user.Context{}
	}
	return &user.Context{
		Org: &user.Org{Id: ctx.OrgID, Name: ctx.OrgID},
		User: &user.User{
			Id:      ctx.UserSubject,
			Org:     ctx.OrgID,
			Name:    ctx.UserName,
			Email:   ctx.UserEmail,
			Status:  user.StatusType(ctx.UserStatus),
			SlackID: ctx.UserSlackID,
			Groups:  ctx.UserGroups,
		},
	}
}

func debugClaims(subject string, claims map[string]any, accessToken *oauth2.Token) {
	log := log.With()
	for claimKey, claimVal := range claims {
		if claimKey == pb.CustomClaimGroups {
			log = log.With(claimKey, fmt.Sprintf("%q", claimVal))
			continue
		}
		log = log.With(claimKey, fmt.Sprintf("%v", claimVal))
	}
	var isJWT bool
	var jwtHeader []byte
	if parts := strings.Split(accessToken.AccessToken, "."); len(parts) == 3 {
		isJWT = true
		jwtHeader, _ = base64.RawStdEncoding.DecodeString(parts[0])
	}
	log.Infof("jwt-access-token=%v, jwt-header=%v, id_token claims=%v, subject=%s, admingroup=%q",
		isJWT, string(jwtHeader),
		len(claims), subject, types.GroupAdmin)
}

func (s *Service) loginOutcome(login *login, outcome outcomeType) {
	login.Outcome = outcome
	if _, err := s.Storage.PersistLogin(login); err != nil {
		log.Warnf("failed persisting login outcome, err=%v", err)
	}
}
