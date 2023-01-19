package client

import (
	"context"

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
		SessionID     string `json:"session_id"     edn:"client/session-id"`
		OrgId         string `json:"-"              edn:"client/org"`
		UserId        string `json:"-"              edn:"client/user"`
		Hostname      string `json:"hostname"       edn:"client/hostname"`
		MachineId     string `json:"machine_id"     edn:"client/machine-id"`
		KernelVersion string `json:"kernel_version" edn:"client/kernel-version"`
		Version       string `json:"version"        edn:"client/version"`
		GoVersion     string `json:"go_version"     edn:"client/go-version"`
		Compiler      string `json:"compiler"       edn:"client/compiler"`
		Platform      string `json:"platform"       edn:"client/platform"`
		Verb          string `json:"verb"           edn:"client/verb"`

		Status       Status          `json:"status"            edn:"client/status"`
		ConnectionId string          `json:"-"                 edn:"client/connection"`
		AgentId      string          `json:"-"                 edn:"client/agent"`
		Context      context.Context `json:"-"                 edn:"-"`
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
