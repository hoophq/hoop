package api

import (
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/domain"
	xtdb "github.com/runopsio/hoop/gateway/storage"
)

type (
	Api struct {
		storage storage
	}

	storage interface {
		Connect() error

		PersistConnection(context *domain.Context, connection *domain.ConnectionOne) (int64, error)
		GetConnections(context *domain.Context) ([]domain.ConnectionList, error)
		GetConnection(context *domain.Context, name string) (*domain.ConnectionOne, error)

		Signup(org *domain.Org, user *domain.User) (int64, error)
		GetLoggedUser(email string) (*domain.Context, error)
	}
)

func NewAPI() (*Api, error) {
	a := &Api{storage: &xtdb.Storage{}}

	if err := a.storage.Connect(); err != nil {
		return nil, err
	}

	a.storage.Signup(&domain.Org{
		Name: "hoop",
	}, &domain.User{
		Name:  "hooper",
		Email: "tester@hoop.dev",
	})

	return a, nil
}

func (a *Api) Authenticate(c *gin.Context) {
	email := "tester@hoop.dev"

	ctx, err := a.storage.GetLoggedUser(email)
	if err != nil {
		c.Error(err)
		return
	}

	c.Set("context", ctx)
	c.Next()
}
