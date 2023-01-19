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

type Client struct {
	hcclient         *plugin.Client
	rpcClient        *rpc.Client
	pluginName       string
	pluginExecPath   string
	pluginEnvVars    map[string]string
	pluginConnConfig map[string]any
	cleanupFn        func(sessionID string)

	SessionCounter int
}

func (c *Client) PluginName() string {
	return c.pluginName
}

func (c *Client) String() string {
	return fmt.Sprintf("plugin=%s | exec=%s", c.pluginName, c.pluginExecPath)
}

// Tells whether or not the underlying process has exited.
func (c *Client) Exited() bool {
	return c.hcclient.Exited()
}

func (c *Client) PluginEnvVars() map[string]string {
	return c.pluginEnvVars
}

func (c *Client) PluginConnectionConfig() map[string]any {
	return c.pluginConnConfig
}

// End the executing subprocess (if it is running) and perform any cleanup
// tasks necessary such as capturing any remaining logs and so on.
//
// This method blocks until the process successfully exits.
//
// This method can safely be called multiple times.
func (c *Client) Kill() {
	c.hcclient.Kill()
}

func (c *Client) callWithTimeout(timeout time.Duration, serviceMethod, sessionID string, args any, reply any) error {
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
func (c *Client) RPCOnSessionOpen(params *pluginhooks.SesssionParams) (*pluginhooks.SessionParamsResponse, error) {
	serviceMethod := fmt.Sprintf("Plugin.%s", pluginhooks.RPCPluginMethodOnSessionOpen)
	var resp pluginhooks.SessionParamsResponse
	return &resp, c.callWithTimeout(onOpenSessionTimeout, serviceMethod, params.SessionID, params, &resp)
}

// RPCOnReceive invokes the OnReceive named function, waits for it to complete, and returns its error status.
func (c *Client) RPCOnReceive(req *pluginhooks.Request) (*pluginhooks.Response, error) {
	serviceMethod := fmt.Sprintf("Plugin.%s", pluginhooks.RPCPluginMethodOnReceive)
	var resp pluginhooks.Response
	return &resp, c.callWithTimeout(onReceiveTimeout, serviceMethod, req.SessionID, req, &resp)
}

// RPCOnSend invokes the OnSend named function, waits for it to complete, and returns its error status.
func (c *Client) RPCOnSend(req *pluginhooks.Request) (*pluginhooks.Response, error) {
	serviceMethod := fmt.Sprintf("Plugin.%s", pluginhooks.RPCPluginMethodOnSend)
	var resp pluginhooks.Response
	return &resp, c.callWithTimeout(onSendTimeout, serviceMethod, req.SessionID, req, &resp)
}

// func (c *Client) ConnectionConfig() string {
// 	config, _ := c.connectConfig.ConnectionConfig["jsonb64"].(string)
// 	return config
// }

type hcPluginClient struct{}

func (hcPluginClient) Server(*plugin.MuxBroker) (interface{}, error) { return nil, nil }
func (hcPluginClient) Client(b *plugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return c, nil
}

func NewClient(
	pluginName, pluginExecPath string,
	pluginEnvVars map[string]string,
	pluginConnConfig map[string]any,
	cleanupFn func(sessionID string)) (*Client, error) {
	magicCookieKey := uuid.NewString()
	magicCookieVal := uuid.NewString()
	pluginVersion := 1
	plugincmd := exec.Command(pluginExecPath)
	plugincmd.Env = []string{
		fmt.Sprintf("PLUGIN_NAME=%s", pluginName),
		fmt.Sprintf("PLUGIN_VERSION=%v", pluginVersion),
		fmt.Sprintf("MAGIC_COOKIE_KEY=%s", magicCookieKey),
		fmt.Sprintf("MAGIC_COOKIE_VAL=%s", magicCookieVal),
	}

	logger := hclog.New(&hclog.LoggerOptions{
		Name:   "host",
		Output: os.Stdout,
		Level:  hclog.Debug,
	})

	// We're a host! Start by launching the plugin process.
	hcclient := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: plugin.HandshakeConfig{
			ProtocolVersion:  uint(pluginVersion),
			MagicCookieKey:   magicCookieKey,
			MagicCookieValue: magicCookieVal,
		},
		// plugins we can dispense.
		Plugins: map[string]plugin.Plugin{
			pluginName: &hcPluginClient{},
		},
		Cmd:    plugincmd,
		Logger: logger,
	})

	// Connect via RPC
	rpcClient, err := hcclient.Client()
	if err != nil {
		return nil, fmt.Errorf("failed connecting via RPC, plugin=%v, err=%v", pluginExecPath, err)
	}

	// Request the plugin
	raw, err := rpcClient.Dispense(pluginName)
	if err != nil {
		return nil, fmt.Errorf("failed dispensing plugin=%v, err=%v", pluginExecPath, err)
	}
	if rclient, _ := raw.(*rpc.Client); rclient != nil {
		return &Client{
			hcclient:         hcclient,
			rpcClient:        rclient,
			pluginName:       pluginName,
			pluginExecPath:   pluginExecPath,
			pluginEnvVars:    pluginEnvVars,
			pluginConnConfig: pluginConnConfig,
			cleanupFn:        cleanupFn,
		}, nil
	}
	return nil, fmt.Errorf("failed to type cast plugin=%v to *rpc.Client, got=%T", pluginExecPath, raw)
}
