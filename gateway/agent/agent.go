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
		Token         string `json:"token"          edn:"xt/id"`
		OrgId         string `json:"-"              edn:"agent/org"`
		Name          string `json:"name"           edn:"agent/name"`
		Hostname      string `json:"hostname"       edn:"agent/hostname"`
		MachineId     string `json:"machine-id"     edn:"agent/machine-id"`
		KernelVersion string `json:"kernel_version" edn:"agent/kernel-version"`
		CreatedById   string `json:"-"              edn:"agent/created-by"`
		Status        Status `json:"status"         edn:"agent/status"`
	}

	Status string
)

const (
	StatusConnected    Status = "CONNECTED"
	StatusDisconnected Status = "DISCONNECTED"
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
