package clientkeys

import (
	"fmt"
	"net/url"
)

var ErrEmpty = fmt.Errorf("dsn is empty")

type DSN struct {
	// http | https | grpc | grpcs |...
	Scheme string
	// host:port
	Address   string
	AgentMode string

	key string
}

func (d *DSN) Key() string { return d.key }

func Parse(clientKeyDsn string) (*DSN, error) {
	if clientKeyDsn == "" {
		return nil, ErrEmpty
	}
	u, err := url.Parse(clientKeyDsn)
	if err != nil {
		return nil, err
	}
	return &DSN{
		Scheme:    u.Scheme,
		Address:   u.Host,
		AgentMode: u.Query().Get("mode"),
		key:       clientKeyDsn}, nil
}
