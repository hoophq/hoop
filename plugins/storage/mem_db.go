package main

import (
	"sync"

	"github.com/runopsio/hoop/domain"
)

type MemDB struct {
	mutex       sync.RWMutex
	Secrets     domain.Secrets
	Connections []domain.Connection
}

var m MemDB

func Connect() error {
	m = MemDB{
		mutex:       sync.RWMutex{},
		Secrets:     make(map[string]interface{}),
		Connections: make([]domain.Connection, 0),
	}
	return nil
}

func GetSecrets() (*domain.Secrets, error) {
	return &m.Secrets, nil
}

func PersistSecrets(secrets domain.Secrets) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.Secrets = secrets
	return nil
}

func GetConnections() ([]domain.Connection, error) {
	return m.Connections, nil
}

func PersistConnection(connection domain.Connection) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.Connections = append(m.Connections, connection)
	return nil
}
