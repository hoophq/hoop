package dcm

import (
	"fmt"
	"reflect"

	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	"github.com/runopsio/hoop/gateway/plugin"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/user"
)

type dcm struct {
	pluginSvc *plugin.Service
}

// CREATE ROLE "_hoop_dcm_user" WITH LOGIN ENCRYPTED PASSWORD '123' VALID UNTIL '2023-05-17 15:15:31.21059-03';
// GRANT SELECT ON ALL TABLES IN SCHEMA public TO "_hoop_dcm_user";
// ALTER USER _hoop_dcm_user VALID UNTIL '2023-05-17 15:24:24.3128-03';

var dropRole = `DROP ROLE IF EXISTS "%s"`
var createRole = `CREATE ROLE "%s" WITH LOGIN ENCRYPTED PASSWORD '%s' VALID UNTIL '%s'`

func New(pluginSvc *plugin.Service) *dcm             { return &dcm{pluginSvc: pluginSvc} }
func (p *dcm) Name() string                          { return plugintypes.PluginDCMName }
func (p *dcm) OnStartup(_ plugintypes.Context) error { return nil }
func (p *dcm) OnConnect(_ plugintypes.Context) error { return nil }
func (p *dcm) OnUpdateConfig(obj plugin.Plugin, old, new *plugin.PluginConfig) error {
	if obj.Name != plugintypes.PluginDCMName {
		return nil
	}
	if old != nil && reflect.DeepEqual(old.EnvVars, new.EnvVars) {
		return nil
	}
	return nil
	// return validatePolicies(new.EnvVars)
}
func (p *dcm) OnReceive(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	if pkt.Type != pbagent.SessionOpen {
		return nil, nil
	}
	pl, err := p.pluginSvc.FindOne(user.NewContext(pctx.OrgID, pctx.UserID), p.Name())
	if err != nil {
		return nil, plugintypes.InternalErr("failed fetching database credentials manager plugin", err)
	}
	policy, err := parsePolicyConfig(pctx.ConnectionName, pl)
	if err != nil {
		return nil, fmt.Errorf("failed parsing policy configuration, reason=%v", err)
	}
	if policy.RenewDuration == "" {
		policy.RenewDuration = "2m"
	}

	encDcmData, err := pb.GobEncode(map[string]any{
		"name":             policy.Name,
		"engine":           policy.Engine,
		"datasource":       policy.datasource,
		"instances":        policy.Instances,
		"grant-privileges": policy.GrantPrivileges,
		"renew-duration":   policy.RenewDuration,
	})
	if err != nil {
		return nil, plugintypes.InternalErr("failed encoding plugin data", err)
	}
	log.Infof("configuring plugin dcm data key!!")
	pkt.Spec[pb.SpecPluginDcmDataKey] = encDcmData
	return nil, nil
}
func (p *dcm) OnDisconnect(_ plugintypes.Context, _ error) error { return nil }
func (p *dcm) OnShutdown()                                       {}
