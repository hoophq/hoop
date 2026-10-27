package login

import (
	"context"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/session"
)

// stubCodec names a protocol so NewServer accepts the lane without the
// real codec linked.
type stubCodec struct{ p inspect.Protocol }

func (c stubCodec) Protocol() inspect.Protocol { return c.p }

func (stubCodec) Decode(inspect.Direction, []byte) ([]inspect.Statement, int, error) {
	return nil, 0, nil
}

// TestDriversRegistered: a lane with Authenticate needs a login driver;
// NewServer refuses the protocol when init registered none.
func TestDriversRegistered(t *testing.T) {
	for _, p := range []inspect.Protocol{inspect.Postgres, inspect.MSSQL, inspect.MongoDB, inspect.Oracle} {
		_, err := proxy.NewServer(proxy.Config{
			Listen: "127.0.0.1:0", Upstream: "127.0.0.1:1", Protocol: p,
			CodecFactory: func() inspect.Codec { return stubCodec{p} },
			Authenticate: func(context.Context, string, string) (session.Identity, proxy.Credential, error) {
				return session.Identity{}, proxy.Credential{}, nil
			},
		})
		if err != nil {
			t.Errorf("%s: no login driver registered: %v", p, err)
		}
	}
}
