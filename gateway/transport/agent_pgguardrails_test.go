package transport

import (
	"encoding/json"
	"testing"

	pgtypes "github.com/hoophq/hoop/common/pgtypes"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// TestListenAgentMessagesPersistsPGGuardRailsInfo: libhoop keeps the
// guardrails info on each packet from the ErrorResponse to the
// ReadyForQuery. Each block must add exactly one guardrails_info entry.
func TestListenAgentMessagesPersistsPGGuardRailsInfo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	const (
		orgID = "00000000-0000-0000-0000-0000000008f6"
		sid   = "00000000-0000-0000-0000-0000000008f7"
	)
	inst := pglitetest.StartMigrated(t)
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'pg-guardrails-test')`, orgID).Error)
	require.NoError(t, models.DB.Exec(`INSERT INTO private.sessions (id, org_id, connection, connection_type, verb, status)
		VALUES (?, ?, 'pg', 'database', 'connect', 'open')`, sid, orgID).Error)

	previous := plugintypes.RegisteredPlugins
	plugintypes.RegisteredPlugins = nil
	t.Cleanup(func() { plugintypes.RegisteredPlugins = previous })

	pctx := &plugintypes.Context{
		SID:                      sid,
		OrgID:                    orgID,
		UserID:                   "user-1",
		ConnectionID:             "conn-1",
		ConnectionName:           "pg",
		ConnectionType:           "database",
		AgentID:                  "agent-1",
		ClientVerb:               pb.ClientVerbConnect,
		ClientOrigin:             pb.ConnectionOriginClient,
		ParamsData:               plugintypes.GenericMap{},
		ExtensionsOnDisconnectFn: func(string) {},
	}
	proxyTransport := &fakeConnectServer{ctx: metadata.NewIncomingContext(
		t.Context(), metadata.Pairs("session-id", sid, "verb", pb.ClientVerbConnect, "origin", pb.ConnectionOriginClient))}
	proxy := streamclient.NewProxy(pctx, proxyTransport)
	require.NoError(t, proxy.Save())
	t.Cleanup(func() { _ = proxy.Close(nil) })

	info := func(rule string) []byte {
		raw, err := json.Marshal([]models.SessionGuardRailsInfo{{
			RuleName:  rule,
			Rule:      models.SessionGuardRailMatchedRule{Type: "deny_words_list", Words: []string{"DROP"}},
			Direction: "input",
		}})
		require.NoError(t, err)
		return raw
	}
	pkt := func(payload, rawInfo []byte) *pb.Packet {
		spec := map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sid),
			pb.SpecClientConnectionID: []byte("1"),
		}
		if rawInfo != nil {
			spec[pb.SpecClientGuardRailsInfoKey] = rawInfo
		}
		return &pb.Packet{Type: pbclient.PGConnectionWrite, Spec: spec, Payload: payload}
	}
	errResp := pgtypes.NewError("%s", "blocked").Encode()
	readyForQuery := []byte{'Z', 0, 0, 0, 5, 'I'}

	// Two blocks: each an ErrorResponse and a ReadyForQuery with the same
	// info, then a ReadyForQuery without info.
	agentTransport := &fakeConnectServer{
		ctx: metadata.NewIncomingContext(t.Context(), metadata.Pairs("connection-name", "pg")),
		recv: []*pb.Packet{
			pkt(errResp, info("rule-1")),
			pkt(readyForQuery, info("rule-1")),
			pkt(readyForQuery, nil),
			pkt(errResp, info("rule-2")),
			pkt(readyForQuery, info("rule-2")),
		},
	}
	agent := streamclient.NewAgent(models.Agent{ID: "agent-1", OrgID: orgID, Name: "agent-1"}, agentTransport)
	_ = (&Server{}).listenAgentMessages(pctx, agent)

	require.Len(t, proxyTransport.sent, 5, "every packet must reach the client")

	var raw []byte
	require.NoError(t, models.DB.Raw(`SELECT guardrails_info FROM private.sessions WHERE id = ?`, sid).Row().Scan(&raw))
	require.NotNil(t, raw, "guardrails_info is null: the block was not saved")
	var got []models.SessionGuardRailsInfo
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Len(t, got, 2, "one entry per block: %s", raw)
	require.Equal(t, "rule-1", got[0].RuleName)
	require.Equal(t, "rule-2", got[1].RuleName)
}

func TestIsPGErrorResponsePacket(t *testing.T) {
	for name, tc := range map[string]struct {
		payload []byte
		want    bool
	}{
		"error response":  {pgtypes.NewError("%s", "x").Encode(), true},
		"ready for query": {[]byte{'Z', 0, 0, 0, 5, 'I'}, false},
		"empty":           {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, isPGErrorResponsePacket(&pb.Packet{Payload: tc.payload}))
		})
	}
}
