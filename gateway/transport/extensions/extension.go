package transportext

import (
	"fmt"

	"github.com/hoophq/hoop/common/memory"
	"github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/models"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var mem = memory.New()

type Context struct {
	SID            string
	OrgID          string
	ConnectionName string
}

func OnReceive(ctx Context, pkt *proto.Packet) error {
	switch pkt.Type {
	case pbagent.SessionOpen:
		conn, err := models.GetConnectionByNameOrID(ctx.OrgID, ctx.ConnectionName)
		if err != nil || conn == nil {
			return fmt.Errorf("unable to obtain connection (empty: %v, name=%v): %v",
				conn == nil, ctx.ConnectionName, err)
		}
		mem.Set(ctx.SID, conn.GuardRailOutputRules)
	case pbclient.WriteStdout, pbclient.WriteStderr:
		outputRules, ok := mem.Get(ctx.SID).([]byte)
		if !ok {
			return nil
		}
		err := Validate("output", outputRules, pkt.Payload)
		switch err.(type) {
		case *ErrRuleMatch:
			return status.Errorf(codes.FailedPrecondition, err.Error())
		case nil:
		default:
			return fmt.Errorf("internal error, failed validating guard rails output rules: %v", err)
		}
	}
	return nil
}

func OnDisconnect(sid string) { mem.Del(sid) }
