package dlp

import (
	"github.com/runopsio/hoop/common/license"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/appconfig"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type plugin struct{}

func New() *plugin                                      { return &plugin{} }
func (p *plugin) Name() string                          { return plugintypes.PluginDLPName }
func (p *plugin) OnStartup(_ plugintypes.Context) error { return nil }
func (p *plugin) OnUpdate(_, _ *types.Plugin) error     { return nil }
func (p *plugin) OnConnect(ctx plugintypes.Context) error {
	isDlpSet := appconfig.Get().GcpDLPJsonCredentials() != ""
	if ctx.OrgLicenseType == license.OSSType && isDlpSet {
		return status.Error(codes.FailedPrecondition, license.ErrDataMaskingUnsupported.Error())
	}
	return nil
}
func (p *plugin) OnReceive(_ plugintypes.Context, _ *pb.Packet) (*plugintypes.ConnectResponse, error) {
	return nil, nil
}
func (p *plugin) OnDisconnect(_ plugintypes.Context, _ error) error { return nil }
func (p *plugin) OnShutdown()                                       {}
