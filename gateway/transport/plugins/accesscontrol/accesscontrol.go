package accesscontrol

import (
	pb "github.com/hoophq/hoop/common/proto"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

const Name string = "access_control"

type plugin struct {
	name string
}

func New() *plugin { return &plugin{name: Name} }

func (r *plugin) Name() string                                   { return r.name }
func (r *plugin) OnStartup(_ plugintypes.Context) error          { return nil }
func (p *plugin) OnUpdate(_, _ plugintypes.PluginResource) error { return nil }
func (r *plugin) OnConnect(_ plugintypes.Context) error          { return nil }
func (r *plugin) OnReceive(_ plugintypes.Context, _ *pb.Packet) (*plugintypes.ConnectResponse, error) {
	return nil, nil
}
func (r *plugin) OnDisconnect(_ plugintypes.Context, _ error) error { return nil }
func (r *plugin) OnShutdown()                                       {}
