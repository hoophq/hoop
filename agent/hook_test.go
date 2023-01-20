package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoophq/pluginhooks"
	"github.com/runopsio/hoop/agent/hook"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
)

type fakeHookClient struct {
	pluginName     string
	sessionParams  *pluginhooks.SessionParamsResponse
	sessionCounter int
	exited         bool
}

func (c *fakeHookClient) PluginParams() *hook.PluginParams {
	return &hook.PluginParams{Name: c.pluginName}
}
func (c *fakeHookClient) SessionCounter() *int { return &c.sessionCounter }
func (c *fakeHookClient) Kill()                {}
func (c *fakeHookClient) Exited() bool         { return c.exited }
func (c *fakeHookClient) RPCOnSessionOpen(params *pluginhooks.SesssionParams) (*pluginhooks.SessionParamsResponse, error) {
	return c.sessionParams, nil
}

func newFakeClient(pluginName string, paramsResp *pluginhooks.SessionParamsResponse, exited bool) hook.Client {
	_, _ = os.Create(filepath.Join("/tmp/", pluginName))
	hookClient, _ := hook.NewClient(hook.ClientConfig{HookClient: &fakeHookClient{
		pluginName:    pluginName,
		sessionParams: paramsResp,
		exited:        exited,
	}})
	return hookClient
}

func newOldStateStore(sessionID string, plugins []hook.Client) memory.Store {
	cl := hook.NewClientList(nil)
	for _, c := range plugins {
		cl.Add(c)
	}
	oldStateStore := memory.New()
	key := fmt.Sprintf(pluginHookSessionsKey, sessionID)
	oldStateStore.Set(key, cl)
	return oldStateStore
}

func TestLoadHooks(t *testing.T) {
	for _, tt := range []struct {
		msg           string
		params        *pb.AgentConnectionParams
		expParamsResp *pluginhooks.SessionParamsResponse
	}{
		{
			msg: "should load all plugins into memory",
			params: &pb.AgentConnectionParams{PluginHookList: []map[string]any{
				{"plugin_name": "p01", "plugin_source": "path:/tmp", "hook_client": newFakeClient("p01", nil, false)},
				{"plugin_name": "p02", "plugin_source": "path:/tmp", "hook_client": newFakeClient("p02", nil, false)},
			}},
		},
		{
			msg: "should load all plugins into memory and mutate params",
			params: &pb.AgentConnectionParams{PluginHookList: []map[string]any{
				{"plugin_name": "p01", "plugin_source": "path:/tmp", "hook_client": newFakeClient("p01", &pluginhooks.SessionParamsResponse{
					ConnectionEnvVars: map[string]any{"SECRET_KEY": "SECRET_VAL"},
					ConnectionCommand: []string{"bash"},
					ClientArgs:        []string{"-l"},
				}, false)},
				{"plugin_name": "p02", "plugin_source": "path:/tmp", "hook_client": newFakeClient("p02", nil, false)},
			}},
			expParamsResp: &pluginhooks.SessionParamsResponse{
				ConnectionEnvVars: map[string]any{"SECRET_KEY": "SECRET_VAL"},
				ConnectionCommand: []string{"bash"},
				ClientArgs:        []string{"-l"},
			},
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			a := &Agent{connStore: memory.New()}
			if err := a.loadHooks("session", tt.params); err != nil {
				t.Fatal(err)
			}
			for _, obj := range a.connStore.List() {
				cl := obj.(*hook.ClientList)
				if len(cl.Items()) != 2 {
					t.Errorf("wrong size of clients, expected=2, found=%v", len(cl.Items()))
				}
			}
			if tt.expParamsResp != nil {
				expClientArgs := strings.Join(tt.expParamsResp.ClientArgs, " ")
				gotClientArgs := strings.Join(tt.params.ClientArgs, " ")
				if expClientArgs != gotClientArgs {
					t.Errorf("expected client_args to be mutate by plugin, got=%v, exp=%v",
						gotClientArgs, expClientArgs)
				}
				expCmdList := strings.Join(tt.expParamsResp.ConnectionCommand, " ")
				gotCmdList := strings.Join(tt.params.CmdList, " ")
				if expCmdList != gotCmdList {
					t.Errorf("expected command to be mutate by plugin, got=%v, exp=%v",
						gotCmdList, expCmdList)
				}

				if len(tt.expParamsResp.ConnectionEnvVars) != len(tt.params.EnvVars) {
					t.Errorf("expected connection env vars to have the same size, got=%v, exp=%v",
						len(tt.params.EnvVars), len(tt.expParamsResp.ConnectionEnvVars))
				}
			}
		})
	}
}

func TestConciliateHooks(t *testing.T) {
	for _, tt := range []struct {
		msg        string
		oldState   memory.Store
		expSession string
		params     *pb.AgentConnectionParams
	}{
		{
			msg:        "it should reuse plugins across sessions",
			oldState:   newOldStateStore("running-session01", []hook.Client{newFakeClient("p01", nil, false)}),
			expSession: "running-session01",
			params: &pb.AgentConnectionParams{PluginHookList: []map[string]any{
				{"plugin_name": "p01", "plugin_source": "path:/tmp", "hook_client": newFakeClient("p01", nil, false)},
			}},
		},
		{
			msg:        "it should replace plugins that are in memory in a exited state",
			oldState:   newOldStateStore("running-session01", []hook.Client{newFakeClient("p01", nil, true)}),
			expSession: "new-session",
			params: &pb.AgentConnectionParams{PluginHookList: []map[string]any{
				{"plugin_name": "p01", "plugin_source": "path:/tmp", "hook_client": newFakeClient("p01", nil, false)},
			}},
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			a := &Agent{connStore: tt.oldState}
			if err := a.loadHooks("new-session", tt.params); err != nil {
				t.Fatal(err)
			}
			if _, ok := a.connStore.Get(fmt.Sprintf(pluginHookSessionsKey, tt.expSession)).(*hook.ClientList); !ok {
				t.Errorf("old state hook not found, exp=%v, store=%v", tt.expSession, a.connStore)
			}
		})
	}
}
