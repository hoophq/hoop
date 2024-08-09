package apiconnections

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"slices"
	"strings"

	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgplugins "github.com/hoophq/hoop/gateway/pgrest/plugins"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

var tagsValRe, _ = regexp.Compile(`^[a-zA-Z0-9_]+(?:[-\.]?[a-zA-Z0-9_]+){0,128}$`)

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

func setConnectionDefaults(req *openapi.Connection) {
	if req.Secrets == nil {
		req.Secrets = map[string]any{}
	}
	var defaultCommand []string
	defaultEnvVars := map[string]any{}
	switch pb.ToConnectionType(req.Type, req.SubType) {
	case pb.ConnectionTypePostgres:
		defaultCommand = []string{"psql", "-v", "ON_ERROR_STOP=1", "-A", "-F\t", "-P", "pager=off", "-h", "$HOST", "-U", "$USER", "--port=$PORT", "$DB"}
	case pb.ConnectionTypeMySQL:
		defaultCommand = []string{"mysql", "-h$HOST", "-u$USER", "--port=$PORT", "-D$DB"}
	case pb.ConnectionTypeMSSQL:
		defaultEnvVars["envvar:INSECURE"] = base64.StdEncoding.EncodeToString([]byte(`false`))
		defaultCommand = []string{
			"sqlcmd", "--exit-on-error", "--trim-spaces", "-r",
			"-S$HOST:$PORT", "-U$USER", "-d$DB", "-i/dev/stdin"}
	case pb.ConnectionTypeOracleDB:
		defaultCommand = []string{"sqlplus", "$USER/$PASS@$HOST:$PORT/$SID"}
	case pb.ConnectionTypeMongoDB:
		defaultEnvVars["envvar:OPTIONS"] = base64.StdEncoding.EncodeToString([]byte(`tls=true`))
		defaultEnvVars["envvar:PORT"] = base64.StdEncoding.EncodeToString([]byte(`27017`))
		defaultCommand = []string{"mongo", "--quiet", "mongodb://$USER:$PASS@$HOST:$PORT/?$OPTIONS"}
		if connStr, ok := req.Secrets["envvar:CONNECTION_STRING"]; ok && connStr != nil {
			defaultEnvVars = nil
			defaultCommand = []string{"mongo", "--quiet", "$CONNECTION_STRING"}
		}
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

func validateConnectionRequest(req openapi.Connection) error {
	errors := []string{}
	if err := apivalidation.ValidateResourceName(req.Name); err != nil {
		errors = append(errors, err.Error())
	}
	for _, val := range req.Tags {
		if !tagsValRe.MatchString(val) {
			errors = append(errors, "tags: values must contain between 1 and 128 alphanumeric characters, it may include (-), (_) or (.) characters")
		}
	}
	if len(errors) > 0 {
		return fmt.Errorf(strings.Join(errors, "; "))
	}
	return nil
}
