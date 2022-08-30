package api

type MemDB struct {
	Secrets     Secrets
	Connections []Connection
}

func (m *MemDB) Connect() error {
	if m.Secrets == nil {
		m.Secrets = make(map[string]interface{})
	}

	if m.Connections == nil {
		m.Connections = make([]Connection, 0)
	}

	return nil
}

func (m *MemDB) GetSecrets() (*Secrets, error) {
	return &m.Secrets, nil
}

func (m *MemDB) PersistSecrets(secrets Secrets) error {
	m.Secrets = secrets
	return nil
}

func (m *MemDB) GetConnections() ([]Connection, error) {
	return m.Connections, nil
}

func (m *MemDB) PersistConnection(connection Connection) error {
	m.Connections = append(m.Connections, connection)
	return nil
}
