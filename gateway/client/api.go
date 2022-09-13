package client

import "github.com/runopsio/hoop/gateway/user"

type (
	Handler struct {
		Service service
	}

	service interface {
		FindAll(context *user.Context) ([]Client, error)
		Persist(client *Client) (int64, error)
	}
)
