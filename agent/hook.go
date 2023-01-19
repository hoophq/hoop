package agent

import (
	"fmt"
	"log"
	"strings"

	"github.com/hoophq/pluginhooks"
	"github.com/runopsio/hoop/agent/hook"
	"github.com/runopsio/hoop/common/clientconfig"
	pb "github.com/runopsio/hoop/common/proto"
	pbclient "github.com/runopsio/hoop/common/proto/client"
)

func loadHookFromSource(pluginRegistryURL, pluginName, pluginSource string) (*hook.PluginManifest, error) {
	switch {
	case strings.HasPrefix(pluginSource, "path:"):
		pluginExecPath := strings.TrimPrefix(pluginSource, "path:")
		return hook.LoadFromLocalPath(pluginName, pluginExecPath)
	default:
		pluginBasePath, err := clientconfig.NewHomeDir("plugins")
		if err != nil {
			return nil, err
		}
		return hook.LoadFromRegistry(pluginRegistryURL, pluginBasePath, pluginSource)
	}
}

func (a *Agent) newHookClient(newHook map[string]any) (*hook.Client, error) {
	pluginName, _ := newHook["plugin_name"].(string)
	if pluginName == "" {
		return nil, fmt.Errorf("plugin on inconsistent state, missing plugin name")
	}
	pluginRegistryURL, _ := newHook["plugin_registry"].(string)
	pluginSource, _ := newHook["plugin_source"].(string)
	pm, err := loadHookFromSource(pluginRegistryURL, pluginName, pluginSource)
	if err != nil {
		return nil, fmt.Errorf("failed loading plugin source (%v), err=%v",
			pluginSource, err)
	}
	pluginEnvVars, _ := newHook["plugin_envvars"].(map[string]string)
	connectionConfig, _ := newHook["connection_config"].(map[string]any)
	client, err := hook.NewClient(
		pluginName,
		pm.ExecFilePath(),
		pluginEnvVars,
		connectionConfig,
		a.sessionCleanup)
	if err != nil {
		return nil, fmt.Errorf("failed initializing plugin client, err=%v", err)
	}
	return client, nil
}

func (a *Agent) executeRPCOnSessionOpen(
	sp *pluginhooks.SesssionParams, client *hook.Client) (*pluginhooks.SessionParamsResponse, error) {
	resp, err := client.RPCOnSessionOpen(sp)
	if err != nil {
		err = fmt.Errorf("plugin %s has rejected the request, reason=%v", client.PluginName(), err)
		log.Printf("session=%s - %s", sp.SessionID, err)
		_ = a.client.Send(&pb.Packet{
			Type:    pbclient.SessionClose,
			Payload: []byte(err.Error()),
			Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(sp.SessionID)},
		})
		return nil, err
	}
	return resp, nil
}

// conciliateHooks will initialize plugins if they aren't started previous
func (a *Agent) conciliateHooks(params *pb.AgentConnectionParams) (*hook.ClientList, error) {
	oldState, _ := a.connStore.Get(pluginHooksKey).(*hook.ClientList)
	newState := hook.NewClientList(params)
	// nothing to conciliate
	if oldState == nil {
		return newState, nil
	}
	for _, newHook := range params.PluginHookList {
		pluginName, _ := newHook["plugin_name"].(string)
		if old, ok := oldState.Get(pluginName); ok {
			if !old.Exited() {
				// keep the old plugin instance in memory
				// prevents from initializing multiple instances of plugins
				newState.Add(old)
				continue
			}
			// these old plugins are already exited
			// initialize new ones
			hookClient, err := a.newHookClient(newHook)
			if err != nil {
				return nil, err
			}
			// replace the plugin when connection config changes
			newState.Add(hookClient)
			// This action will make old clients
			// using this plugin to disconnect.
			go old.Kill()
			continue
		}
		hookClient, err := a.newHookClient(newHook)
		if err != nil {
			return nil, err
		}
		newState.Add(hookClient)
	}
	return newState, nil
}

func (a *Agent) loadHooks(sessionID string, params *pb.AgentConnectionParams) error {
	hookList, err := a.conciliateHooks(params)
	if err != nil {
		log.Printf("session=%v - failed conciliating plugin hooks, err=%v", sessionID, err)
		return err
	}
	if hookList.Empty() {
		// nothing to conciliate, just add them in the list
		for _, newHook := range params.PluginHookList {
			hookClient, err := a.newHookClient(newHook)
			if err != nil {
				return err
			}
			hookList.Add(hookClient)
		}
	}

	sessionHookItems := hook.NewClientList(params)
	// call on session open for each plugin available in this session
	for _, currentHook := range params.PluginHookList {
		pluginName, _ := currentHook["plugin_name"].(string)
		if pluginName == "" {
			return fmt.Errorf("inconsistent plugin entry, missing plugin_name attribute")
		}
		if hookClient, exists := hookList.Get(pluginName); exists {
			resp, err := a.executeRPCOnSessionOpen(&pluginhooks.SesssionParams{
				SessionID:         sessionID,
				PluginEnvVars:     hookClient.PluginEnvVars(),
				UserID:            params.UserID,
				ConnectionName:    params.ConnectionName,
				ConnectionType:    params.ConnectionType,
				ConnectionEnvVars: params.EnvVars,
				ConnectionConfig:  hookClient.PluginConnectionConfig(),
				ConnectionCommand: params.CmdList,
				ClientArgs:        params.ClientArgs,
				ClientVerb:        params.ClientVerb,
			}, hookClient)
			if err != nil {
				if hookClient.SessionCounter <= 0 {
					go hookClient.Kill()
				}
				// clean previous plugins initialized by this session
				defer func() { a.sessionCleanup(sessionID) }()
				break
			}
			hookClient.SessionCounter++
			sessionHookItems.Add(hookClient)
			mutateParams(params, resp)
		}
	}
	storeKey := fmt.Sprintf(pluginHookSessionsKey, sessionID)
	a.connStore.Set(storeKey, sessionHookItems)
	a.connStore.Set(pluginHooksKey, hookList)
	return nil
}

func mutateParams(params *pb.AgentConnectionParams, resp *pluginhooks.SessionParamsResponse) {
	if resp == nil {
		return
	}
	if resp.ClientArgs != nil {
		params.ClientArgs = resp.ClientArgs
	}
	if resp.ConnectionCommand != nil {
		params.CmdList = resp.ConnectionCommand
	}
	if resp.ConnectionEnvVars != nil {
		params.EnvVars = resp.ConnectionEnvVars
	}
}
