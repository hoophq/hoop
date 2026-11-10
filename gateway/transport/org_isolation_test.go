package transport

import (
	"context"
	"strings"
	"testing"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	authinterceptor "github.com/hoophq/hoop/gateway/transport/interceptors/auth"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
	transportsystem "github.com/hoophq/hoop/gateway/transport/system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	isoOrgA = "00000000-0000-0000-0000-00000000111a"
	isoOrgB = "00000000-0000-0000-0000-00000000111b"
)

func startIsolationDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	for _, id := range []string{isoOrgA, isoOrgB} {
		require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, ?)`, id, "org-"+id).Error)
	}
	previous := plugintypes.RegisteredPlugins
	plugintypes.RegisteredPlugins = nil
	t.Cleanup(func() { plugintypes.RegisteredPlugins = previous })
}

func newIsolationProxy(t *testing.T, orgID, sid, agentID string) *streamclient.ProxyStream {
	t.Helper()
	pctx := &plugintypes.Context{
		SID:                      sid,
		OrgID:                    orgID,
		UserID:                   "user-1",
		ConnectionID:             "conn-1",
		ConnectionName:           "pg",
		ConnectionType:           "database",
		AgentID:                  agentID,
		ClientVerb:               pb.ClientVerbConnect,
		ClientOrigin:             pb.ConnectionOriginClient,
		ParamsData:               plugintypes.GenericMap{},
		ExtensionsOnDisconnectFn: func(string) {},
	}
	transport := &fakeConnectServer{ctx: metadata.NewIncomingContext(
		t.Context(), metadata.Pairs("session-id", sid, "verb", pb.ClientVerbConnect, "origin", pb.ConnectionOriginClient))}
	proxy := streamclient.NewProxy(pctx, transport)
	require.NoError(t, proxy.Save())
	t.Cleanup(func() { _ = proxy.Close(nil) })
	return proxy
}

// An org must not reach the agent of another org, also through a row that
// already points to it.
func TestAgentDispatchIsOrgScoped(t *testing.T) {
	startIsolationDB(t)
	agentB := models.Agent{
		OrgID: isoOrgB,
		ID:    "00000000-0000-0000-0000-0000000011b1",
		Name:  "_default",
		Mode:  pb.AgentModeStandardType,
	}
	agentTransport := &fakeConnectServer{ctx: metadata.NewIncomingContext(
		t.Context(), metadata.Pairs("origin", pb.ConnectionOriginAgent))}
	agentStream := streamclient.NewAgent(agentB, agentTransport)
	require.NoError(t, agentStream.Save())
	t.Cleanup(func() { _ = agentStream.Close(plugintypes.Context{}, nil) })

	streamID := streamtypes.NewStreamID(agentB.ID, "")
	assert.NotNil(t, streamclient.GetAgentStream(isoOrgB, streamID))
	assert.Nil(t, streamclient.GetAgentStream(isoOrgA, streamID))
	assert.Nil(t, streamclient.GetAgentStream("", streamID))
	assert.False(t, streamclient.IsAgentOnline(isoOrgA, streamID))

	proxy := newIsolationProxy(t, isoOrgA, "00000000-0000-0000-0000-0000000011a1", agentB.ID)
	assert.False(t, proxy.IsAgentOnline())
	assert.ErrorIs(t, proxy.SendToAgent(&pb.Packet{Type: "test"}), pb.ErrAgentOffline)

	bare := transportsystem.BareExecWithTimeout(isoOrgA,
		&pbsystem.BareExecRequest{SID: "probe-a", AgentID: agentB.ID, Script: "id"}, time.Second)
	assert.Equal(t, pbsystem.StatusFailedType, bare.Status)
	assert.Contains(t, bare.Output, "agent stream not found")

	plan := transportsystem.RunPgManagerPlan(isoOrgA, agentB.ID, &pbsystem.PgManagerPlanRequest{SID: "plan-a"})
	assert.Contains(t, plan.Message, "agent stream not found")
	apply := transportsystem.RunPgManagerApply(isoOrgA, agentB.ID, &pbsystem.PgManagerApplyRequest{SID: "apply-a"})
	assert.Contains(t, apply.Message, "agent stream not found")
	prov := transportsystem.RunDBProvisioner(isoOrgA, agentB.ID, &pbsystem.DBProvisionerRequest{SID: "prov-a"})
	assert.Contains(t, prov.Message, "agent stream not found")
	hook := transportsystem.RunRunbookHook(isoOrgA, agentB.ID, &pbsystem.RunbookHookRequest{ID: "hook-a", SID: "hook-a"})
	assert.Contains(t, hook.Output, "agent stream not found")

	assert.Empty(t, agentTransport.sent, "org A sent packets to the agent of org B")

	own := transportsystem.BareExecWithTimeout(isoOrgB,
		&pbsystem.BareExecRequest{SID: "probe-b", AgentID: agentB.ID, Script: "id"}, 50*time.Millisecond)
	assert.Contains(t, own.Output, "timeout", "the owner org still reaches its agent")
	assert.Len(t, agentTransport.sent, 1)
}

func TestConnectRefusesForeignSessionID(t *testing.T) {
	startIsolationDB(t)
	const (
		storedSID = "00000000-0000-0000-0000-0000000011b2"
		liveSID   = "00000000-0000-0000-0000-0000000011b3"
	)
	require.NoError(t, models.DB.Exec(`INSERT INTO private.sessions (id, org_id, connection, connection_type, verb, status)
		VALUES (?, ?, 'pg', 'database', 'connect', 'open')`, storedSID, isoOrgB).Error)
	newIsolationProxy(t, isoOrgB, liveSID, "00000000-0000-0000-0000-0000000011b1")

	for _, sid := range []string{
		storedSID,
		liveSID,
		strings.ToUpper(storedSID),
		strings.ReplaceAll(storedSID, "-", ""),
		"{" + storedSID + "}",
		"urn:uuid:" + storedSID,
		"0000-0000-0000-0000-0000-0000-0000-11b2",
		"not-a-uuid",
	} {
		ctx := context.WithValue(t.Context(), authinterceptor.GatewayContextKey{}, &authinterceptor.GatewayContext{
			UserContext: models.Context{OrgID: isoOrgA, UserID: "user-a"},
			Connection:  types.ConnectionInfo{Name: "pg-a", AccessModeExec: "enabled", AccessModeConnect: "enabled"},
		})
		stream := &fakeConnectServer{ctx: metadata.NewIncomingContext(ctx, metadata.Pairs(
			"origin", pb.ConnectionOriginClient, "verb", pb.ClientVerbConnect, "session-id", sid))}
		err := (&Server{}).Connect(stream)
		assert.Equal(t, codes.InvalidArgument, status.Code(err), "session %v: %v", sid, err)
		assert.Contains(t, err.Error(), "invalid session id")
		assert.Empty(t, stream.sent)
	}

	var orgID string
	require.NoError(t, models.DB.Raw(`SELECT org_id FROM private.sessions WHERE id = ?`, storedSID).Scan(&orgID).Error)
	assert.Equal(t, isoOrgB, orgID)
	assert.Equal(t, isoOrgB, streamclient.GetProxyStream(liveSID).PluginContext().OrgID)

	for _, tc := range []struct{ orgID, sid string }{
		{isoOrgB, storedSID},
		{isoOrgB, liveSID},
		{isoOrgA, "00000000-0000-0000-0000-0000000011a9"},
		{isoOrgA, ""},
	} {
		assert.NoError(t, refuseForeignSession(tc.orgID, tc.sid), "org %v, session %q", tc.orgID, tc.sid)
	}
}
