package services

import (
	"context"
	"sync"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/clientexec"
)

type ExecOptions struct {
	OrgID                     string
	SessionID                 string
	ConnectionName            string
	ConnectionCommandOverride []string
	BearerToken               string
	Verb                      string
	UserAgent                 string
	Origin                    string
	ImpersonateUserSubject    string

	Script     string
	EnvVars    map[string]string
	ClientArgs []string
}

func Exec(ctx context.Context, opts ExecOptions) (*clientexec.Response, error) {
	log := log.With("sid", opts.SessionID)

	client, err := clientexec.New(&clientexec.Options{
		OrgID:                     opts.OrgID,
		SessionID:                 opts.SessionID,
		ConnectionName:            opts.ConnectionName,
		ConnectionCommandOverride: opts.ConnectionCommandOverride,
		BearerToken:               opts.BearerToken,
		UserAgent:                 opts.UserAgent,
		Verb:                      opts.Verb,
		Origin:                    opts.Origin,
		ImpersonateUserSubject:    opts.ImpersonateUserSubject,
	})
	if err != nil {
		return nil, err
	}

	var closeOnce sync.Once
	closeClient := func() {
		closeOnce.Do(func() {
			client.Close()
		})
	}

	log.Infof("started runexec method for connection %v", opts.ConnectionName)

	respCh := make(chan *clientexec.Response, 1)

	go func() {
		defer closeClient()

		respCh <- client.Run(
			[]byte(opts.Script),
			opts.EnvVars,
			opts.ClientArgs...,
		)
	}()

	select {
	case resp := <-respCh:
		return resp, nil

	case <-ctx.Done():
		closeClient()
		return nil, ctx.Err()
	}
}
