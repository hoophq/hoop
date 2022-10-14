package security

import (
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/user"
	"golang.org/x/oauth2"
	"strings"
)

type (
	Service struct {
		Storage     storage
		UserService UserService
		Provider    *idp.Provider
	}

	storage interface {
		PersistLogin(login *login) (int64, error)
		FindLogin(state string) (*login, error)
	}

	UserService interface {
		FindBySub(sub string) (*user.Context, error)
		Persist(u interface{}) error
	}

	login struct {
		Id       string      `edn:"xt/id"`
		Email    string      `edn:"login/email"`
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

func (s *Service) Login(email, redirect string) (string, error) {
	login := &login{
		Id:       uuid.NewString(),
		Email:    email,
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
	login, err := s.Storage.FindLogin(state)
	if err != nil {
		if login != nil {
			s.loginOutcome(login, outcomeError)
		}
		return login.Redirect + "?error=unexpected_error"
	}

	token, idToken, err := s.exchangeCodeByToken(code)
	if err != nil {
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=unexpected_error"
	}

	var profile map[string]interface{}
	if err := idToken.Claims(&profile); err != nil {
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=unexpected_error"
	}

	email, ok := profile["email"].(string)
	if !ok || email != login.Email {
		s.loginOutcome(login, outcomeEmailMismatch)
		return login.Redirect + "?error=email_mismatch"
	}

	sub, ok := profile["sub"].(string)
	if !ok || sub == "" {
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=unexpected_error"
	}

	if strings.Contains(s.Provider.Domain, "okta") {
		sub = email
	}

	context, err := s.UserService.FindBySub(sub)
	if err != nil {
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=unexpected_error"
	}

	if context.Org == nil || context.User == nil {
		if err := s.signup(context, sub, profile); err != nil {
			s.loginOutcome(login, outcomeError)
			return login.Redirect + "?error=unexpected_error"
		}
	}

	if context.User.Status != user.StatusActive {
		s.loginOutcome(login, pendingReviewError)
		return login.Redirect + "?error=pending_review"
	}

	s.loginOutcome(login, outcomeSuccess)
	return login.Redirect + "?token=" + token.AccessToken
}

func (s *Service) exchangeCodeByToken(code string) (*oauth2.Token, *oidc.IDToken, error) {
	token, err := s.Provider.Exchange(s.Provider.Context, code)
	if err != nil {
		return nil, nil, err
	}

	idToken, err := s.Provider.VerifyIDToken(token)
	if err != nil {
		return nil, nil, err
	}

	return token, idToken, nil
}

func (s *Service) signup(context *user.Context, sub string, profile map[string]interface{}) error {
	email := profile["email"].(string)
	newOrg := false

	if context.Org == nil {
		org, ok := profile["https://hoophq.dev/org"].(string)
		if !ok || org == "" {
			org = extractDomain(email)
		}

		context.Org = &user.Org{
			Id:   uuid.NewString(),
			Name: org,
		}

		if err := s.UserService.Persist(context.Org); err != nil {
			return err
		}

		newOrg = true
	}

	if context.User == nil {
		groups := make([]string, 0)
		status := user.StatusReviewing

		if newOrg {
			status = user.StatusActive
			groups = append(groups, user.GroupAdmin)
		}

		context.User = &user.User{
			Id:     sub,
			Org:    context.Org.Id,
			Name:   profile["name"].(string),
			Email:  email,
			Status: status,
			Groups: groups,
		}

		if err := s.UserService.Persist(context.User); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) loginOutcome(login *login, outcome outcomeType) {
	login.Outcome = outcome
	s.Storage.PersistLogin(login)
}

func extractDomain(email string) string {
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
