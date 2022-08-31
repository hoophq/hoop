package api

import (
	"fmt"
	"github.com/runopsio/hoop/domain"
	"plugin"
)

type (
	Api struct {
		storage storage
	}

	storage struct {
		connect           func() error
		getSecrets        func() (*domain.Secrets, error)
		persistSecrets    func(secrets domain.Secrets) error
		getConnections    func() ([]domain.Connection, error)
		persistConnection func(connection domain.Connection) error
	}
)

func NewAPI(storagePlugin string) (*Api, error) {
	p, err := plugin.Open(storagePlugin)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	connectFn, err := p.Lookup("Connect")
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	getSecretsFn, err := p.Lookup("GetSecrets")
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	persistSecretsFn, err := p.Lookup("PersistSecrets")
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	getConnectionsFn, err := p.Lookup("GetConnections")
	if err != nil {
		fmt.Println(err)
	}

	persistConnectionFn, err := p.Lookup("PersistConnection")
	if err != nil {
		fmt.Println(err)
	}

	a := &Api{storage: storage{
		connect:           connectFn.(func() error),
		getSecrets:        getSecretsFn.(func() (*domain.Secrets, error)),
		persistSecrets:    persistSecretsFn.(func(secrets domain.Secrets) error),
		getConnections:    getConnectionsFn.(func() ([]domain.Connection, error)),
		persistConnection: persistConnectionFn.(func(connection domain.Connection) error),
	}}

	if err := a.storage.connect(); err != nil {
		return nil, err
	}

	return a, nil
}
