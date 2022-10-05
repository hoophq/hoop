package plugin

import (
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Service struct {
		Storage storage
	}

	storage interface {
		Persist(context *user.Context, c *Plugin) (int64, error)
		FindAll(context *user.Context) ([]Plugin, error)
		FindOne(context *user.Context, name string) (*Plugin, error)
	}

	Plugin struct {
		Id            string       `json:"id"          edn:"xt/id"`
		OrgId         string       `json:"-"           edn:"plugin/org"`
		Name          string       `json:"name"        edn:"plugin/name"          binding:"required"`
		Connections   []Connection `json:"connections" edn:"plugin/connections"   binding:"required"`
		InstalledById string       `json:"-"           edn:"plugin/installed-by"`
	}

	Connection struct {
		Id     string              `json:"id"      edn:"id"      binding:"required"`
		Name   string              `json:"name"    edn:"name"`
		Config []string            `json:"config"  edn:"config"  binding:"required"`
		Groups map[string][]string `json:"groups"  edn:"groups"`
	}
)

func (s *Service) FindAll(context *user.Context) ([]Plugin, error) {
	return s.Storage.FindAll(context)
}

func (s *Service) Persist(context *user.Context, c *Plugin) (int64, error) {
	c.Id = uuid.NewString()
	return s.Storage.Persist(context, c)
}

func (s *Service) FindOne(context *user.Context, name string) (*Plugin, error) {
	return s.Storage.FindOne(context, name)
}
