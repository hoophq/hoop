package transportsystem

import (
	"fmt"

	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
)

func KillSession(pctx plugintypes.Context, sid string) error {
	client := streamclient.GetProxyStream(sid)
	//sid, connectionstype, subtype, clientOrigin
	if client == nil {
		// best effort to flush the logs if session not found in memory
		for _, plugin := range plugintypes.RegisteredPlugins {
			if plugin.Name() != plugintypes.PluginAuditName {
				continue
			}
			// OnDisconnect will flush the WAL logs
			err := plugin.OnDisconnect(pctx,
				fmt.Errorf("Logs flushed not found in disk"))
			if err != nil {
				return fmt.Errorf("failed processing audit plugin on-disconnect, err=%v", err)
			}

		}
		return nil
	}
	_ = client.Close(fmt.Errorf("session killed by the user"))
	return nil
}
