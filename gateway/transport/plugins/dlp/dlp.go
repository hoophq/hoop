package dlp

import (
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

type plugin struct{}

func New() *plugin                                      { return &plugin{} }
func (p *plugin) Name() string                          { return plugintypes.PluginDLPName }
func (p *plugin) OnStartup(_ plugintypes.Context) error { return nil }
func (p *plugin) OnUpdate(_, _ *types.Plugin) error     { return nil }
func (p *plugin) OnConnect(_ plugintypes.Context) error { return nil }
func (p *plugin) OnReceive(_ plugintypes.Context, _ *pb.Packet) (*plugintypes.ConnectResponse, error) {
	return nil, nil
}
func (p *plugin) OnDisconnect(_ plugintypes.Context, _ error) error { return nil }
func (p *plugin) OnShutdown()                                       {}
