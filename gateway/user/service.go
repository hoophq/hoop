package user

import (
	"context"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/idp"
)

type (
	Service struct {
		Storage       storage
		Authenticator *idp.Authenticator
	}

	storage interface {
		PersistLogin(login *login) (int64, error)
		FindLogin(state string) (*login, error)
		Signup(org *Org, user *User) (txId int64, err error)
		ContextByEmail(email string) (*Context, error)
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
		Id    string `json:"id"    edn:"xt/id"`
		Org   string `json:"-"     edn:"user/org"`
		Name  string `json:"name"  edn:"user/name"`
		Email string `json:"email" edn:"user/email" binding:"required"`
	}

	login struct {
		Id       string      `edn:"xt/id"`
		Email    string      `edn:"login/email"`
		Redirect string      `edn:"login/redirect"`
		Type     loginType   `edn:"login/type"`
		Outcome  outcomeType `edn:"login/outcome"`
	}

	loginType   string
	outcomeType string
)

const (
	typeLogin  loginType = "login"
	typeSignup loginType = "signup"

	outcomeSuccess outcomeType = "success"
	outcomeFailure outcomeType = "failure"
)

func (s *Service) Signup(org *Org, user *User) (txId int64, err error) {
	return s.Storage.Signup(org, user)
}

func (s *Service) ContextByEmail(email string) (*Context, error) {
	return s.Storage.ContextByEmail(email)
}

func (s *Service) Login(email, redirect string) (string, error) {
	login := &login{
		Id:       uuid.NewString(),
		Email:    email,
		Type:     typeLogin,
		Redirect: redirect,
	}

	_, err := s.Storage.PersistLogin(login)
	if err != nil {
		return "", err
	}

	return s.Authenticator.AuthCodeURL(login.Id), nil
}

func (s *Service) Callback(state, code string) (string, error) {
	login, err := s.Storage.FindLogin(state)
	if err != nil {
		return "", err
	}

	ctx := context.Background()
	token, err := s.Authenticator.Exchange(ctx, code)
	if err != nil {
		return "", err
	}

	idToken, err := s.Authenticator.VerifyIDToken(ctx, token)
	if err != nil {
		return "", err
	}

	var profile map[string]interface{}
	if err := idToken.Claims(&profile); err != nil {
		return "", err
	}

	return login.Redirect, nil
}
