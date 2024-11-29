package apiconnections

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
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
			"sqlcmd", "--exit-on-error", "--trim-spaces", "-s\t", "-r",
			"-S$HOST:$PORT", "-U$USER", "-d$DB", "-i/dev/stdin"}
	case pb.ConnectionTypeOracleDB:
		defaultEnvVars["envvar:LD_LIBRARY_PATH"] = base64.StdEncoding.EncodeToString([]byte(`/opt/oracle/instantclient_19_24`))
		defaultCommand = []string{"sqlplus", "-s", "$USER/$PASS@$HOST:$PORT/$SID"}
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

var reSanitize, _ = regexp.Compile(`^[a-zA-Z0-9_]+(?:[-\.]?[a-zA-Z0-9_]+){1,128}$`)
var errInvalidOptionVal = errors.New("option values must contain between 1 and 127 alphanumeric characters, it may include (-), (_) or (.) characters")

func validateListOptions(urlValues url.Values) (o models.ConnectionFilterOption, err error) {
	if reSanitize == nil {
		return o, fmt.Errorf("failed compiling sanitize regex on listing connections")
	}
	for key, values := range urlValues {
		switch key {
		case "agent_id":
			o.AgentID = values[0]
		case "type":
			o.Type = values[0]
		case "subtype":
			o.SubType = values[0]
		case "managed_by":
			o.ManagedBy = values[0]
		case "tags":
			if len(values[0]) > 0 {
				for _, tagVal := range strings.Split(values[0], ",") {
					if !reSanitize.MatchString(tagVal) {
						return o, errInvalidOptionVal
					}
					o.Tags = append(o.Tags, tagVal)
				}
			}
			continue
		default:
			continue
		}
		if !reSanitize.MatchString(values[0]) {
			return o, errInvalidOptionVal
		}
	}
	return
}

func getAccessToken(c *gin.Context) string {
	tokenHeader := c.GetHeader("authorization")
	apiKey := c.GetHeader("Api-Key")
	if apiKey != "" {
		return apiKey
	}
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) > 1 {
		return tokenParts[1]
	}
	return ""
}

func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

// getBool retorna um booleano do map
func getBool(m map[string]interface{}, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return v == "YES" || v == "true" || v == "t" || v == "1"
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	default:
		return false
	}
}

func getEnvValue(envs map[string]string, key string) string {
	if val, exists := envs[key]; exists {
		decoded, err := base64.StdEncoding.DecodeString(val)
		if err != nil {
			return ""
		}
		return string(decoded)
	}
	return ""
}

func getMongoDBFromConnectionString(connStr string) string {
	// Decodifica a connection string que está em base64
	decoded, err := base64.StdEncoding.DecodeString(connStr)
	if err != nil {
		return ""
	}
	mongoURL := string(decoded)

	// Se a URL não começa com mongodb://, não é uma connection string válida
	if !strings.HasPrefix(mongoURL, "mongodb://") {
		return ""
	}

	// Parse da URL para extrair o database
	u, err := url.Parse(mongoURL)
	if err != nil {
		return ""
	}

	// O database vem depois da última barra e antes da query string
	path := u.Path
	if path == "" || path == "/" {
		return ""
	}

	// Remove a barra inicial se existir
	return strings.TrimPrefix(path, "/")
}
