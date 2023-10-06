package transportv2

import (
	"strings"

	apitypes "github.com/runopsio/hoop/gateway/apiclient/types"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type ClientContext struct {
	UserContext          types.APIContext
	Connection           types.ConnectionInfo
	BearerToken          string
	PostSaveSessionToken string
	IsAdminExec          bool

	sessionID string
	verb      string
}

func (c *ClientContext) ValidateConnectionAttrs() error {
	if c.Connection.Name == "" || c.Connection.AgentID == "" ||
		c.Connection.ID == "" || c.Connection.Type == "" ||
		c.Connection.AgentMode == "" {
		return status.Error(codes.InvalidArgument, "missing required connection attributes")
	}
	return nil
}

type AgentContext struct {
	Agent       *apitypes.Agent
	ApiURL      string
	BearerToken string
}

func mdget(md metadata.MD, metaName string) string {
	data := md.Get(metaName)
	if len(data) == 0 {
		// keeps compatibility with old clients that
		// pass headers with underline. HTTP headers are not
		// accepted with underline for some servers, e.g.: nginx
		data = md.Get(strings.ReplaceAll(metaName, "-", "_"))
		if len(data) == 0 {
			return ""
		}
	}
	return data[0]
}
