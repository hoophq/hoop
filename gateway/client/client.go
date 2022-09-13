package client

import (
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Service struct {
		Storage storage
	}

	storage interface {
		FindAll(context *user.Context) ([]Client, error)
		Persist(client *Client) (int64, error)
	}

	Client struct {
		Id            string `json:"-"              edn:"xt/id"`
		OrgId         string `json:"-"              edn:"client/org"`
		UserId        string `json:"-"              edn:"client/user"`
		Hostname      string `json:"hostname"       edn:"client/hostname"`
		MachineId     string `json:"machine-id"     edn:"client/machine-id"`
		KernelVersion string `json:"kernel_version" edn:"client/kernel-version"`
		Status        Status `json:"status"         edn:"client/status"`
		ConnectionId  string `json:"-"              edn:"client/connection"`
		AgentId       string `json:"-"              edn:"client/agent"`
	}

	Status string
)

const (
	StatusConnected    Status = "CONNECTED"
	StatusDisconnected Status = "DISCONNECTED"
)

func (s *Service) Persist(client *Client) (int64, error) {
	return s.Storage.Persist(client)
}

func (s *Service) FindAll(context *user.Context) ([]Client, error) {
	return s.Storage.FindAll(context)
}
