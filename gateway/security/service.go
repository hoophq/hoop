package security

import (
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/security/idp"
	"golang.org/x/oauth2"
)

type (
	Service struct {
		Storage  storage
		Provider *idp.Provider
	}

	storage interface {
		PersistLogin(login *login) (int64, error)
		FindLogin(state string) (*login, error)
	}

	login struct {
		Id       string      `edn:"xt/id"`
		Email    string      `edn:"login/email"`
		Redirect string      `edn:"login/redirect"`
		Outcome  outcomeType `edn:"login/outcome"`
	}

	outcomeType string

	Context struct {
		Org  *Org
		User *User
	}

	Org struct {
		Id   string `json:"id"   edn:"xt/id"`
		Name string `json:"name" edn:"org/name" binding:"required"`
	}

	User struct {
		Id    string `json:"id"    edn:"xt/id"`
		Org   string `json:"-"     edn:"user/org"`
		Name  string `json:"name"  edn:"user/name"`
		Email string `json:"email" edn:"user/email" binding:"required"`
	}
)

const (
	outcomeSuccess       outcomeType = "success"
	outcomeError         outcomeType = "error"
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

func (s *Service) Callback(state, code string) (string, error) {
	login, err := s.Storage.FindLogin(state)
	if err != nil {
		if login != nil {
			s.loginOutcome(login, outcomeError)
		}
		return "", err
	}

	token, err := s.Provider.Exchange(s.Provider.Context, code)
	if err != nil {
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=" + err.Error(), nil
	}

	idToken, err := s.Provider.VerifyIDToken(token)
	if err != nil {
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=" + err.Error(), nil
	}

	var profile map[string]interface{}
	if err := idToken.Claims(&profile); err != nil {
		s.loginOutcome(login, outcomeError)
		return login.Redirect + "?error=" + err.Error(), nil
	}

	//if profile["email"] != login.Email {
	//	s.loginOutcome(login, outcomeEmailMismatch)
	//	return login.Redirect + "?error=email_mismatch", nil
	//}

	s.loginOutcome(login, outcomeSuccess)
	return login.Redirect + "?token=" + token.AccessToken, nil
}

func (s *Service) loginOutcome(login *login, outcome outcomeType) {
	login.Outcome = outcome
	s.Storage.PersistLogin(login)
}
