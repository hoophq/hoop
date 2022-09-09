package api

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/domain"
	xtdb "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Api struct {
		AgentService agent.Service
		storage      storage
	}

	storage interface {
		Connect() error

		Signup(org *user.Org, user *user.User) (int64, error)
		GetLoggedUser(email string) (*user.Context, error)

		PersistConnection(context *user.Context, connection *domain.Connection) (int64, error)
		GetConnections(context *user.Context) ([]domain.ConnectionList, error)
		GetConnection(context *user.Context, name string) (*domain.Connection, error)
	}
)

func createTrialEntities(api *Api) error {
	orgId := uuid.New().String()
	userId := uuid.New().String()

	org := user.Org{
		Id:   orgId,
		Name: "hoop",
	}

	user := user.User{
		Id:    userId,
		Org:   orgId,
		Name:  "hooper",
		Email: "tester@hoop.dev",
	}

	agent := agent.Agent{
		Token:       "x-agt-test-token",
		Name:        "test-agent",
		OrgId:       orgId,
		CreatedById: userId,
	}

	_, err := api.storage.Signup(&org, &user)
	_, err = api.AgentService.Persist(&agent)

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
