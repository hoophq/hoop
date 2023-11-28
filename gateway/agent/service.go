package agent

import (
	"strings"

	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Service struct {
		Storage storage
	}

	storage interface {
		Persist(agent *Agent) (int64, error)
		FindAll(context *user.Context) ([]Agent, error)
		FindByNameOrID(ctx *user.Context, name string) (*Agent, error)
		FindByToken(token string) (*Agent, error)
		Evict(ctx *user.Context, xtID string) error
	}

	Agent struct {
		Id            string `json:"id"             edn:"xt/id"`
		Token         string `json:"token"          edn:"agent/token"`
		OrgId         string `json:"-"              edn:"agent/org"`
		Name          string `json:"name"           edn:"agent/name"`
		Mode          string `json:"mode"           edn:"agent/mode"`
		Hostname      string `json:"hostname"       edn:"agent/hostname"`
		MachineId     string `json:"machine-id"     edn:"agent/machine-id"`
		KernelVersion string `json:"kernel_version" edn:"agent/kernel-version"`
		Version       string `json:"version"        edn:"agent/version"`
		GoVersion     string `json:"go_version"     edn:"agent/go-version"`
		Compiler      string `json:"compiler"       edn:"agent/compiler"`
		Platform      string `json:"platform"       edn:"agent/platform"`
		Status        Status `json:"status"         edn:"agent/status"`
	}

	Status string
)

const (
	StatusConnected    Status = "CONNECTED"
	StatusDisconnected Status = "DISCONNECTED"
)

func (s *Service) FindByNameOrID(ctx *user.Context, name string) (*Agent, error) {
	agt, err := s.Storage.FindByNameOrID(ctx, name)
	setAgentModeDefault(agt)
	return agt, err
}

func (s *Service) FindByToken(token string) (*Agent, error) {
	agt, err := s.Storage.FindByToken(token)
	setAgentModeDefault(agt)
	return agt, err
}

func (s *Service) Persist(agent *Agent) (int64, error) {
	return s.Storage.Persist(agent)
}

func (s *Service) FindAll(context *user.Context) ([]Agent, error) {
	result, err := s.Storage.FindAll(context)
	if err != nil {
		return nil, err
	}
	for i := range result {
		result[i].Token = ""
		setAgentModeDefault(&result[i])
	}
	return result, nil
}

func (s *Service) Evict(ctx *user.Context, xtID string) error {
	return s.Storage.Evict(ctx, xtID)
}

// set to default mode if the entity doesn't contain any value
func setAgentModeDefault(agt *Agent) {
	if agt != nil && agt.Mode == "" {
		agt.Mode = pb.AgentModeStandardType
	}
}

func deterministicAgentUUID(orgID, agentName string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(
		strings.Join([]string{"agent", orgID, agentName}, "/"))).String()
}
