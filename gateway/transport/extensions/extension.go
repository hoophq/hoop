package transportext

import (
	"fmt"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	"github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/guardrails"
	"github.com/hoophq/hoop/gateway/jira"
	"github.com/hoophq/hoop/gateway/models"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var mem = memory.New()

type Context struct {
	SID                                 string
	OrgID                               string
	AgentID                             string
	ConnectionName                      string
	ConnectionType                      string
	ConnectionSubType                   string
	ConnectionEnvs                      map[string]any
	ConnectionJiraTransitionNameOnClose string
	UserEmail                           string
	Verb                                string
}

func OnReceive(ctx Context, pkt *proto.Packet) error {
	if ctx.Verb == proto.ClientVerbPlainExec {
		return nil
	}
	switch pkt.Type {
	case pbagent.SessionOpen:
		processEventOpenSessionHook(ctx, pkt)
		conn, err := models.GetConnectionGuardRailRules(ctx.OrgID, ctx.ConnectionName)
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
		err := guardrails.Validate("output", outputRules, pkt.Payload)
		switch err.(type) {
		case *guardrails.ErrRuleMatch:
			return status.Errorf(codes.FailedPrecondition, err.Error())
		case nil:
		default:
			return fmt.Errorf("internal error, failed validating guard rails output rules: %v", err)
		}
	case pbclient.SessionClose:
		processEventCloseSessiontHook(ctx, pkt)
		jiraConf, err := models.GetJiraIntegration(ctx.OrgID)
		if err != nil {
			log.With("sid", ctx.SID).Errorf("unable to obtain jira integration configuration, reason=%v", err)
			return status.Errorf(codes.Internal, "unable to get jira integration configuration")
		}
		if jiraConf == nil || !jiraConf.IsActive() {
			break
		}
		jiraIssueKey, err := models.GetSessionJiraIssueByID(ctx.OrgID, ctx.SID)
		if err != nil && err != models.ErrNotFound {
			log.With("sid", ctx.SID).Errorf("unable to obtain jira issue key from session, reason=%v", err)
			return status.Errorf(codes.Internal, "unable to obtain jira issue key from session")
		}
		if jiraIssueKey == "" {
			break
		}
		err = jira.TransitionIssue(jiraConf, jiraIssueKey, ctx.ConnectionJiraTransitionNameOnClose)
		if err != nil {
			log.With("sid", ctx.SID).Warn(err)
		}
		log.With("sid", ctx.SID).Debugf("jira transitioned status to %s, key=%v, success=%v",
			ctx.ConnectionJiraTransitionNameOnClose, jiraIssueKey, err == nil)
	}
	return nil
}

// OnDisconnect remove any object from memory identified by this sid
func OnDisconnect(sid string) { mem.Del(sid) }
