package hook

import (
	"github.com/google/uuid"
	"github.com/hoophq/pluginhooks"
	pb "github.com/runopsio/hoop/common/proto"
)

type ClientList struct {
	id     string
	items  map[string]*Client
	params *pb.AgentConnectionParams
}

func NewClientList(params *pb.AgentConnectionParams) *ClientList {
	return &ClientList{id: uuid.NewString(), items: map[string]*Client{}, params: params}
}

func (l *ClientList) ConnectionParams() *pb.AgentConnectionParams {
	return l.params
}

func (l *ClientList) Add(c *Client) {
	l.items[c.pluginName] = c
}

func (l *ClientList) Get(pluginName string) (*Client, bool) {
	item, ok := l.items[pluginName]
	return item, ok
}

func (l *ClientList) Items() map[string]*Client {
	return l.items
}

func (l *ClientList) Empty() bool {
	return len(l.items) == 0
}

// ExecRPCOnSend execute all onsend rpc methods for each loaded plugin
func (l *ClientList) ExecRPCOnSend(req *pluginhooks.Request) ([]byte, error) {
	return l.execRPCOnSendRecv("onsend", req)
}

// ExecRPCOnRecv execute all onreceive rpc methods for each loaded plugin
func (l *ClientList) ExecRPCOnRecv(req *pluginhooks.Request) ([]byte, error) {
	return l.execRPCOnSendRecv("onreceive", req)
}

func (p *ClientList) execRPCOnSendRecv(method string, req *pluginhooks.Request) ([]byte, error) {
	respPayload := req.Payload
	for _, hook := range p.items {
		var resp *pluginhooks.Response
		var err error
		if method == "onsend" {
			resp, err = hook.RPCOnSend(&pluginhooks.Request{
				SessionID:  req.SessionID,
				Payload:    respPayload,
				PacketType: req.PacketType,
			})
		} else {
			resp, err = hook.RPCOnReceive(&pluginhooks.Request{
				SessionID:  req.SessionID,
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
