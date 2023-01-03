package hook

import (
	"encoding/gob"
	"fmt"
	"net/rpc"
	"os"
	"os/exec"

	"github.com/google/uuid"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/hoophq/pluginhooks"
	pb "github.com/runopsio/hoop/common/proto"
)

func init() {
	// required for go RPC methods
	gob.Register(map[string]any{})
}

type Client struct {
	hcclient       *plugin.Client
	rpcClient      *rpc.Client
	pluginName     string
	pluginExecPath string
	connectConfig  *pluginhooks.Config
}

type List struct {
	id     string
	items  map[string]*Client
	params *pb.AgentConnectionParams
}

func NewClientList(params *pb.AgentConnectionParams) *List {
	return &List{id: uuid.NewString(), items: map[string]*Client{}, params: params}
}

func (l *List) ConnectionParams() *pb.AgentConnectionParams {
	return l.params
}

// ID is used to identified the state of this object for comparing them
func (l *List) ID() string {
	return l.id
}

func (l *List) SetID(id string) {
	l.id = id
}

func (l *List) AddItem(pluginName string, c *Client) {
	l.items[pluginName] = c
}

func (l *List) GetItem(pluginName string) (*Client, bool) {
	item, ok := l.items[pluginName]
	return item, ok
}

func (l *List) Items() map[string]*Client {
	return l.items
}

// ExecRPCOnSend execute all onsend rpc methods for each loaded plugin
func (l *List) ExecRPCOnSend(req *pluginhooks.Request) ([]byte, error) {
	return l.execRPCOnSendRecv("onsend", req)
}

// ExecRPCOnRecv execute all onreceive rpc methods for each loaded plugin
func (l *List) ExecRPCOnRecv(req *pluginhooks.Request) ([]byte, error) {
	return l.execRPCOnSendRecv("onreceive", req)
}

// Close cleanup the process and connection with loaded plugins
func (l *List) Close() error {
	for _, hook := range l.items {
		hook.Kill()
	}
	return nil
}

func (p *List) execRPCOnSendRecv(method string, req *pluginhooks.Request) ([]byte, error) {
	respPayload := req.Payload
	for _, hook := range p.items {
		var resp *pluginhooks.Response
		var err error
		if method == "onsend" {
			resp, err = hook.RPCOnSend(&pluginhooks.Request{
				Payload:    respPayload,
				PacketType: req.PacketType,
			})
		} else {
			resp, err = hook.RPCOnReceive(&pluginhooks.Request{
				Payload:    respPayload,
				PacketType: req.PacketType,
			})
		}
		if err != nil {
			return nil, err
		}
		if len(resp.Payload) > 0 {
			// use the last packet if a next plugin exists
			respPayload = resp.Payload
		}
	}
	return respPayload, nil
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

// End the executing subprocess (if it is running) and perform any cleanup
// tasks necessary such as capturing any remaining logs and so on.
//
// This method blocks until the process successfully exits.
//
// This method can safely be called multiple times.
func (c *Client) Kill() {
	c.hcclient.Kill()
}

// RPCOnConnect invokes the OnConnect named function, waits for it to complete, and returns its error status.
func (c *Client) RPCOnConnect() error {
	serviceMethod := fmt.Sprintf("Plugin.%s", pluginhooks.RPCPluginMethodOnConnect)
	return c.rpcClient.Call(serviceMethod, c.connectConfig, &pluginhooks.Empty{})
}

// RPCOnReceive invokes the OnReceive named function, waits for it to complete, and returns its error status.
func (c *Client) RPCOnReceive(req *pluginhooks.Request) (*pluginhooks.Response, error) {
	serviceMethod := fmt.Sprintf("Plugin.%s", pluginhooks.RPCPluginMethodOnReceive)
	var resp pluginhooks.Response
	return &resp, c.rpcClient.Call(serviceMethod, req, &resp)
}

// RPCOnSend invokes the OnSend named function, waits for it to complete, and returns its error status.
func (c *Client) RPCOnSend(req *pluginhooks.Request) (*pluginhooks.Response, error) {
	serviceMethod := fmt.Sprintf("Plugin.%s", pluginhooks.RPCPluginMethodOnSend)
	var resp pluginhooks.Response
	// c.rpcClient.C
	return &resp, c.rpcClient.Call(serviceMethod, req, &resp)
}

func (c *Client) ConnectionConfig() string {
	config, _ := c.connectConfig.ConnectionConfig["jsonb64"].(string)
	return config
}

type hcPluginClient struct{}

func (hcPluginClient) Server(*plugin.MuxBroker) (interface{}, error) { return nil, nil }
func (hcPluginClient) Client(b *plugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return c, nil
}

func NewClient(pluginName, pluginExecPath string, config *pluginhooks.Config) (*Client, error) {
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
			hcclient:       hcclient,
			rpcClient:      rclient,
			pluginName:     pluginName,
			pluginExecPath: pluginExecPath,
			connectConfig:  config,
		}, nil
	}
	return nil, fmt.Errorf("failed to type cast plugin=%v to *rpc.Client, got=%T", pluginExecPath, raw)
}
