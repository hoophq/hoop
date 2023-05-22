package dcm

import (
	"fmt"
	"hash/crc32"
	"reflect"
	"strings"

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

	// pl.Connections
	// base64.StdEncoding.DecodeString(policyEnc)
	// get policy and merge with policy from connection
	//
	// pctx.Conn
	// 200213
	// dbHostEnc, ok := pctx.ConnectionSecret["envvar:HOST"]
	// if !ok {
	// 	return nil, fmt.Errorf("missing required secret (HOST) in connection")
	// }
	// dbHostKeyBytes, err := base64.StdEncoding.DecodeString(fmt.Sprintf("%v", dbHostEnc))
	// if err != nil {
	// 	return nil, plugintypes.InternalErr("failed decoding (base64) connection host key", err)
	// }

	// dbHostKey := strings.ToUpper(string(dbHostKeyBytes))
	// connectionStringEnc := pl.Config.EnvVars[dbHostKey]
	// connectionStringBytes, err := base64.StdEncoding.DecodeString(connectionStringEnc)
	// if err != nil {
	// 	return nil, plugintypes.InternalErr("failed decoding (base64) master database config", err)
	// }
	// if string(connectionStringBytes) == "" {
	// 	return nil, fmt.Errorf("database master credentials is empty for host %v", dbHostKey)
	// }
	// var masterDbConfig map[string]string
	// if err := json.Unmarshal(masterDbConfigBytes, &masterDbConfig); err != nil {
	// 	return nil, plugintypes.InternalErr("failed decoding (json) master database config", err)
	// }
	// masterDbConfig["masteruser"]
	// masterDbConfig["masterpwd"]
	// masterDbConfig["masterport"]
	// if masterDbConfig["masterhost"] != dbHost {
	// 	return fmt.Errorf("master database host does not match with connection secret HOST")
	// }
	// var privileges []string
	// for _, conn := range pl.Connections {
	// 	for _, name := range conn.Config {
	// 		name = strings.TrimSpace(name)
	// 		if name == "" {
	// 			continue
	// 		}
	// 		_, ok := grantPrivileges[name]
	// 		if !ok {
	// 			return nil, fmt.Errorf("privilege %q is not allowed, possible options are: %v",
	// 				name, grantPrivileges)
	// 		}
	// 		privileges = append(privileges, name)
	// 	}
	// }

	// use the default role instead
	// if privStringEnc, ok := pl.Config.EnvVars[privilegeConfigNameKey]; ok && len(privileges) == 0 {
	// 	privilegeBytes, err := base64.StdEncoding.DecodeString(privStringEnc)
	// 	if err != nil {
	// 		return nil, plugintypes.InternalErr("failed decoding (base64) default roles", err)
	// 	}
	// 	for _, name := range strings.Split(string(privilegeBytes), ";") {
	// 		name = strings.TrimSpace(name)
	// 		if name == "" {
	// 			continue
	// 		}
	// 		_, ok := grantPrivileges[name]
	// 		if !ok {
	// 			return nil, fmt.Errorf("privilege %q is not allowed, possible options are: %v",
	// 				name, grantPrivileges)
	// 		}
	// 		privileges = append(privileges, name)
	// 	}
	// }
	// sort.Strings(privileges)
	// if len(privileges) == 0 {
	// 	return nil, fmt.Errorf("the plugin or connection requires at least one grant priveleges")
	// }

	// revoketAt := time.Now().UTC()
	// sessionUser := newCrc32(pctx.ConnectionName, privileges)
	// sessionUserPwd := uuid.NewString()
	// ttl := time.Second * 20
	// revokeAt := time.Now().UTC().Add(ttl).Format(time.RFC3339)
	// var statements []string
	// switch pctx.ConnectionType {
	// case pb.ConnectionTypeMySQL:
	// case pb.ConnectionTypePostgres:
	// 	statements = []string{
	// 		fmt.Sprintf(dropRole, sessionUser),
	// 		fmt.Sprintf(createRole,
	// 			sessionUser,
	// 			sessionUserPwd,
	// 			revokeAt),
	// 		// `CREATE ROLE '_hoop_' WITH LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}';`,
	// 		// `GRANT SELECT ON ALL TABLES IN SCHEMA public TO "{{name}}"`,
	// 	}
	// default:
	// 	return nil, fmt.Errorf("connection type %q not supported", pctx.ConnectionType)
	// }

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

func newCrc32(connName string, privileges []string) string {
	t := crc32.MakeTable(crc32.IEEE)
	data := fmt.Sprintf("%s:%s", connName, strings.Join(privileges, ","))
	return fmt.Sprintf("_hoop_session_user_%08x", crc32.Checksum([]byte(data), t))
}
