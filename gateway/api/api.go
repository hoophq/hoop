package api

type (
	Api struct {
		repository repository
	}

	repository interface {
		Connect() error

		GetSecrets() (*Secrets, error)
		PersistSecrets(secrets Secrets) error

		GetConnections() ([]Connection, error)
		PersistConnection(connection Connection) error
	}
)

func NewAPI() (*Api, error) {
	a := &Api{repository: &MemDB{}}

	if err := a.repository.Connect(); err != nil {
		return nil, err
	}

	return a, nil
}
