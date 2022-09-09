package api

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/domain"
	xtdb "github.com/runopsio/hoop/gateway/storage"
)

type (
	Api struct {
		storage storage
	}

	storage interface {
		Connect() error

		Signup(org *domain.Org, user *domain.User) (int64, error)
		GetLoggedUser(email string) (*domain.Context, error)

		PersistConnection(context *domain.Context, connection *domain.Connection) (int64, error)
		GetConnections(context *domain.Context) ([]domain.ConnectionList, error)
		GetConnection(context *domain.Context, name string) (*domain.Connection, error)

		PersistAgent(agent *domain.Agent) (int64, error)
		GetAgents(context *domain.Context) ([]domain.Agent, error)
	}
)

func createTrialEntities(api *Api) error {
	orgId := uuid.New().String()
	userId := uuid.New().String()

	org := domain.Org{
		Id:   orgId,
		Name: "hoop",
	}

	user := domain.User{
		Id:    userId,
		Org:   orgId,
		Name:  "hooper",
		Email: "tester@hoop.dev",
	}

	agent := domain.Agent{
		Token:       "x-agt-test-token",
		Name:        "test-agent",
		OrgId:       orgId,
		CreatedById: userId,
	}

	_, err := api.storage.Signup(&org, &user)
	_, err = api.storage.PersistAgent(&agent)

	if err != nil {
		return err
	}

	return nil
}

func NewAPI() (*Api, error) {
	a := &Api{storage: &xtdb.Storage{}}

	if err := a.storage.Connect(); err != nil {
		return nil, err
	}

	if err := createTrialEntities(a); err != nil {
		return nil, err
	}

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
