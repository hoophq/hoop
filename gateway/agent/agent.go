package agent

import "github.com/runopsio/hoop/gateway/user"

type (
	Service struct {
		Storage storage
	}

	storage interface {
		Persist(agent *Agent) (int64, error)
		FindAll(context *user.Context) ([]Agent, error)
		FindOne(token string) (*Agent, error)
	}

	Agent struct {
		Token       string `json:"token"    edn:"xt/id"`
		OrgId       string `json:"-"        edn:"Agent/org"`
		Name        string `json:"name"     edn:"Agent/name"`
		Hostname    string `json:"hostname" edn:"Agent/hostname"`
		CreatedById string `json:"-"        edn:"Agent/created-by"`
	}
)

func (s *Service) FindOne(token string) (*Agent, error) {
	return s.Storage.FindOne(token)
}

func (s *Service) Persist(agent *Agent) (int64, error) {
	return s.Storage.Persist(agent)
}

func (s *Service) FindAll(context *user.Context) ([]Agent, error) {
	return s.Storage.FindAll(context)
}
