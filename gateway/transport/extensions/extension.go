package transportext

import (
	"fmt"

	"github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/models"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
		err = Validate("input", conn.GuardRailInputRules, pkt.Payload)
		switch err.(type) {
		case *ErrRuleMatch:
			return status.Errorf(codes.FailedPrecondition, err.Error())
		case nil:
		default:
			return fmt.Errorf("internal error, failed validating guard rails input rules: %v", err)
		}
	case pbclient.WriteStdout, pbclient.WriteStderr:
		conn, err := models.GetConnectionByNameOrID(ctx.OrgID, ctx.ConnectionName)
		if err != nil || conn == nil {
			return fmt.Errorf("unable to obtain connection (empty: %v, name=%v): %v",
				conn == nil, ctx.ConnectionName, err)
		}
		err = Validate("output", conn.GuardRailOutputRules, pkt.Payload)
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
