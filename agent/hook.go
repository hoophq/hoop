package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/hoophq/pluginhooks"
	"github.com/runopsio/hoop/agent/hook"
	"github.com/runopsio/hoop/common/clientconfig"
	pb "github.com/runopsio/hoop/common/proto"
)

func (a *Agent) killHookPlugins(sessionID string) {
	if _, ph := a.connectionParams(sessionID); ph != nil {
		_ = ph.Close()
	}
}

func (a *Agent) newHookClient(sessionID string, params *pb.AgentConnectionParams, newHook map[string]any) (*hook.Client, error) {
	pluginHomeDir, err := clientconfig.NewHomeDir("plugins")
	if err != nil {
		return nil, err
	}
	pluginName, _ := newHook["plugin_name"].(string)
	if pluginName == "" {
		return nil, fmt.Errorf("plugin on inconsistent state, missing 'plugin_name' key")
	}
	connectionConfig, _ := newHook["connection_config"].(map[string]any)
	pluginExec := filepath.Join(pluginHomeDir, pluginName)
	execInfo, err := os.Stat(pluginExec)
	if err != nil {
		return nil, fmt.Errorf("session=%s - failed loading plugin exec (%v), err=%v",
			sessionID, pluginExec, err)
	}
	log.Printf("session=%s - plugin exec %v loaded, mode=%v",
		sessionID, pluginExec, execInfo.Mode().String())
	connectionEnvVars := params.EnvVars
	// avoid registering agent.connEnv with gob.Register(...)
	if params.ConnectionType == pb.ConnectionTypePostgres || params.ConnectionType == pb.ConnectionTypeTCP {
		connectionEnvVars = map[string]any{}
	}

	client, err := hook.NewClient(pluginName, pluginExec,
		&pluginhooks.Config{
			SessionID:         sessionID,
			UserID:            params.UserID,
			ConnectionName:    params.ConnectionName,
			ConnectionType:    params.ConnectionType,
			ConnectionEnvVars: connectionEnvVars,
			ConnectionConfig:  connectionConfig,
			ConnectionCommand: params.CmdList,
			ClientArgs:        params.ClientArgs,
			// TODO: implement
			ClientVerb: "",
		})
	if err != nil {
		return nil, fmt.Errorf("failed initializing plugin client, err=%v", err)
	}
	return client, nil
}

func (a *Agent) executeRPCOnConnect(sessionID string, client *hook.Client) error {
	if err := client.RPCOnConnect(); err != nil {
		go client.Kill()
		// TODO: use the response to add a friendly error!
		err = fmt.Errorf("plugin %s has rejected the request, reason=%v", client.PluginName(), err)
		log.Printf("session=%s - %s", sessionID, err)
		_ = a.client.Send(&pb.Packet{
			Type:    pb.PacketClientAgentConnectErrType.String(),
			Payload: []byte(err.Error()),
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)},
		})
		return err
	}
	return nil
}

func (a *Agent) loadHooks(sessionID string, params *pb.AgentConnectionParams) error {
	currentHookList := params.PluginHookList
	newState := hook.NewClientList(params)
	oldState, _ := a.connStore.Get(pluginHooksKey).(*hook.List)
	// oldState, setterFn := loadPluginHooks(a.connStore, params.ConnectionName)
	if oldState != nil {
		for _, newHook := range currentHookList {
			pluginName, _ := newHook["plugin_name"].(string)
			if old, ok := oldState.GetItem(pluginName); ok {
				// if old, ok := oldState.items[pluginName]; ok {
				newConnectionConfig, ok := newHook["connection_config"].(map[string]any)
				if !ok {
					return fmt.Errorf("plugin on inconsistent state, missing 'connection_config' key")
				}
				if newConnectionConfig["jsonb64"] == old.ConnectionConfig() {
					// if newConnectionConfig["jsonb64"] == old.connectConfig.ConnectionConfig["jsonb64"] {
					if !old.Exited() {
						// keep the old plugin instance in memory
						// prevents from initializing multiple instances of plugins
						newState.AddItem(pluginName, old)
						newState.SetID(oldState.ID())
						continue
					}
				}
				hookClient, err := a.newHookClient(sessionID, params, newHook)
				if err != nil {
					return err
				}
				// replace the plugin when connection config changes
				newState.AddItem(pluginName, hookClient)
				// This action will make old clients
				// using this plugin to disconnect.
				go old.Kill()
				continue
			}
			hookClient, err := a.newHookClient(sessionID, params, newHook)
			if err != nil {
				return err
			}
			newState.AddItem(pluginName, hookClient)
		}
		for _, hookClient := range newState.Items() {
			if err := a.executeRPCOnConnect(sessionID, hookClient); err != nil {
				return err
			}
		}
		storeKey := fmt.Sprintf(pluginHookSessionsKey, sessionID)
		a.connStore.Set(storeKey, newState)
		a.connStore.Set(pluginHooksKey, newState)
		return nil
	}
	for _, newHook := range currentHookList {
		hookClient, err := a.newHookClient(sessionID, params, newHook)
		if err != nil {
			return err
		}
		if err := a.executeRPCOnConnect(sessionID, hookClient); err != nil {
			return err
		}
		pluginName, _ := newHook["plugin_name"].(string)
		newState.AddItem(pluginName, hookClient)
	}

	storeKey := fmt.Sprintf(pluginHookSessionsKey, sessionID)
	a.connStore.Set(storeKey, newState)
	a.connStore.Set(pluginHooksKey, newState)
	return nil
}
