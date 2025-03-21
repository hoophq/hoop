package transportsystem

import (
	"fmt"

	"github.com/hoophq/hoop/gateway/transport/streamclient"
)

func KillSession(sid string) error {
	client := streamclient.GetProxyStream(sid)
	if client == nil {
		return fmt.Errorf("session not found in memory")
	}
	_ = client.Close(fmt.Errorf("session killed by the user"))
	return nil
}
