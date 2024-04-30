package apiconnections

import (
	"encoding/base64"
	"fmt"
	"slices"

	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgplugins "github.com/runopsio/hoop/gateway/pgrest/plugins"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

func accessControlAllowed(ctx pgrest.Context) (func(connName string) bool, error) {
	p, err := pgplugins.New().FetchOne(ctx, plugintypes.PluginAccessControlName)
	if err != nil {
		return nil, err
	}
	if p == nil || ctx.IsAdmin() {
		return func(_ string) bool { return true }, nil
	}

	return func(connName string) bool {
		for _, c := range p.Connections {
			if c.Name == connName {
				for _, userGroup := range ctx.GetUserGroups() {
					if allow := slices.Contains(c.Config, userGroup); allow {
						return allow
					}
				}
				return false
			}
		}
		return false
	}, nil
}

func setConnectionDefaults(req *Connection) {
	if req.Secrets == nil {
		req.Secrets = map[string]any{}
	}
	var defaultCommand []string
	defaultEnvVars := map[string]any{}
	switch pb.ToConnectionType(req.Type, req.SubType) {
	case pb.ConnectionTypePostgres:
		defaultCommand = []string{"psql", "-A", "-F\t", "-P", "pager=off", "-h", "$HOST", "-U", "$USER", "--port=$PORT", "$DB"}
	case pb.ConnectionTypeMySQL:
		defaultCommand = []string{"mysql", "-h$HOST", "-u$USER", "--port=$PORT", "-D$DB"}
	case pb.ConnectionTypeMSSQL:
		defaultEnvVars["envvar:INSECURE"] = base64.StdEncoding.EncodeToString([]byte(`false`))
		defaultCommand = []string{
			"sqlcmd", "--exit-on-error", "--trim-spaces", "-r",
			"-S$HOST:$PORT", "-U$USER", "-d$DB", "-i/dev/stdin"}
	case pb.ConnectionTypeMongoDB:
		defaultEnvVars["envvar:TLS"] = base64.StdEncoding.EncodeToString([]byte(`true`))
		defaultCommand = []string{"mongo", "--quiet", "mongodb://$USER:$PASS@$HOST:$PORT/$DB?tls=true"}
	}

	if len(req.Command) == 0 {
		req.Command = defaultCommand
	}

	for key, val := range defaultEnvVars {
		if _, isset := req.Secrets[key]; !isset {
			req.Secrets[key] = val
		}
	}
}

func coerceToMapString(src map[string]any) map[string]string {
	dst := map[string]string{}
	for k, v := range src {
		dst[k] = fmt.Sprintf("%v", v)
	}
	return dst
}

func coerceToAnyMap(src map[string]string) map[string]any {
	dst := map[string]any{}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
