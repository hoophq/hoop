package security

import (
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/user"
	"golang.org/x/oauth2"
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

	context, err := s.UserService.FindBySub(profile["sub"].(string))
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
	}

	context.User = &user.User{
		Id:     sub,
		Org:    context.Org.Id,
		Name:   profile["name"].(string),
		Email:  email,
		Status: user.StatusActive,
	}

	return s.UserService.Persist(context.User)
}

func (s *Service) loginOutcome(login *login, outcome outcomeType) {
	login.Outcome = outcome
	s.Storage.PersistLogin(login)
}

func extractDomain(email string) string {
	return "hoop"
}
