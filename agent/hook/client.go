package hook

import (
	"encoding/gob"
	"fmt"
	"net/rpc"
	"os"
	"os/exec"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/hoophq/pluginhooks"
)

func init() {
	// required for go RPC methods
	gob.Register(map[string]any{})
	gob.Register(map[string]string{})
}

const (
	onOpenSessionTimeout = time.Second * 5
	onSendTimeout        = time.Second * 2
	onReceiveTimeout     = time.Second * 2
)

type Client interface {
	PluginParams() *PluginParams
	Exited() bool
	Kill()
	SessionCounter() *int
	RPCOnSessionOpen(params *pluginhooks.SesssionParams) (*pluginhooks.SessionParamsResponse, error)
}

type PluginParams struct {
	Name             string
	ExecPath         string
	EnvVars          map[string]string
	ConnectionConfig map[string]any
}

type ClientConfig struct {
	PluginParams
	CleanupFn func(sessionID string)

	HookClient Client
}

type client struct {
	hcclient     *plugin.Client
	rpcClient    *rpc.Client
	cleanupFn    func(sessionID string)
	pluginParams *PluginParams

	sessionCounter int
}

func (c *client) PluginParams() *PluginParams {
	return c.pluginParams
}

func (c *client) SessionCounter() *int {
	return &c.sessionCounter
}

// Tells whether or not the underlying process has exited.
func (c *client) Exited() bool {
	return c.hcclient.Exited()
}

// End the executing subprocess (if it is running) and perform any cleanup
// tasks necessary such as capturing any remaining logs and so on.
//
// This method blocks until the process successfully exits.
//
// This method can safely be called multiple times.
func (c *client) Kill() {
	c.hcclient.Kill()
}

func (c *client) callWithTimeout(timeout time.Duration, serviceMethod, sessionID string, args any, reply any) error {
	rpcCall := c.rpcClient.Go(serviceMethod, args, reply, make(chan *rpc.Call, 1))
	select {
	case <-time.After(timeout):
		c.cleanupFn(sessionID)
		return fmt.Errorf("method %s timeout (%v)", serviceMethod, timeout)
	case resp := <-rpcCall.Done:
		if resp != nil && resp.Error != nil {
			c.cleanupFn(sessionID)
			return resp.Error
		}
	}
	return nil
}

// RPCOnSessionOpen invokes the OnSessionOpen named function, waits for it to complete, and returns its error status.
func (c *client) RPCOnSessionOpen(params *pluginhooks.SesssionParams) (*pluginhooks.SessionParamsResponse, error) {
	serviceMethod := fmt.Sprintf("Plugin.%s", pluginhooks.RPCPluginMethodOnSessionOpen)
	var resp pluginhooks.SessionParamsResponse
	return &resp, c.callWithTimeout(onOpenSessionTimeout, serviceMethod, params.SessionID, params, &resp)
}

// RPCOnReceive invokes the OnReceive named function, waits for it to complete, and returns its error status.
func (c *client) RPCOnReceive(req *pluginhooks.Request) (*pluginhooks.Response, error) {
	serviceMethod := fmt.Sprintf("Plugin.%s", pluginhooks.RPCPluginMethodOnReceive)
	var resp pluginhooks.Response
	return &resp, c.callWithTimeout(onReceiveTimeout, serviceMethod, req.SessionID, req, &resp)
}

// RPCOnSend invokes the OnSend named function, waits for it to complete, and returns its error status.
func (c *client) RPCOnSend(req *pluginhooks.Request) (*pluginhooks.Response, error) {
	serviceMethod := fmt.Sprintf("Plugin.%s", pluginhooks.RPCPluginMethodOnSend)
	var resp pluginhooks.Response
	return &resp, c.callWithTimeout(onSendTimeout, serviceMethod, req.SessionID, req, &resp)
}

type hcPluginClient struct{}

func (hcPluginClient) Server(*plugin.MuxBroker) (interface{}, error) { return nil, nil }
func (hcPluginClient) Client(b *plugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return c, nil
}

func NewClient(config ClientConfig) (Client, error) {
	if config.HookClient != nil {
		return config.HookClient, nil
	}
	if config.Name == "" || config.ExecPath == "" {
		return nil, fmt.Errorf("missing required plugin name and exec path")
	}
	hookClient := &client{
		pluginParams: &config.PluginParams,
		cleanupFn:    config.CleanupFn,
	}
	magicCookieKey := uuid.NewString()
	magicCookieVal := uuid.NewString()
	protocolVersion := 1

	plugincmd := exec.Command(config.ExecPath)
	plugincmd.Env = []string{
		fmt.Sprintf("PLUGIN_NAME=%s", config.PluginParams.Name),
		fmt.Sprintf("PLUGIN_VERSION=%v", protocolVersion),
		fmt.Sprintf("MAGIC_COOKIE_KEY=%s", magicCookieKey),
		fmt.Sprintf("MAGIC_COOKIE_VAL=%s", magicCookieVal),
	}

	logger := hclog.New(&hclog.LoggerOptions{
		Name:   "host",
		Output: os.Stdout,
		Level:  hclog.Debug,
	})

	// We're a host! Start by launching the plugin process.
	hookClient.hcclient = plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: plugin.HandshakeConfig{
			ProtocolVersion:  uint(protocolVersion),
			MagicCookieKey:   magicCookieKey,
			MagicCookieValue: magicCookieVal,
		},
		// plugins we can dispense.
		Plugins: map[string]plugin.Plugin{
			config.Name: &hcPluginClient{},
		},
		Cmd:    plugincmd,
		Logger: logger,
	})

	// Connect via RPC
	rpcClient, err := hookClient.hcclient.Client()
	if err != nil {
		return nil, fmt.Errorf("failed connecting via RPC, plugin=%s, err=%v",
			config.ExecPath, err)
	}

	// Request the plugin
	raw, err := rpcClient.Dispense(config.Name)
	if err != nil {
		return nil, fmt.Errorf("failed dispensing plugin=%v, err=%v", config.ExecPath, err)
	}
	if rclient, _ := raw.(*rpc.Client); rclient != nil {
		hookClient.rpcClient = rclient
		return hookClient, nil
	}
	return nil, fmt.Errorf("failed to type cast plugin=%v to *rpc.Client, got=%T",
		config.ExecPath, raw)
}
