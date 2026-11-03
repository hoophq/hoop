package transport

import (
	"bytes"
	"encoding/binary"
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
	// A pipelined batch: libhoop's redactor flushes the held-back messages,
	// the guardrail ErrorResponse and the ReadyForQuery in one packet.
	combined := bytes.Join([][]byte{dataRow, commandComplete, parseComplete, bindComplete, errResp, readyForQuery}, nil)

	// Two blocks: each an ErrorResponse and a ReadyForQuery with the same
	// info, then a ReadyForQuery without info. Then a third block in one
	// combined packet.
	agentTransport := &fakeConnectServer{
		ctx: metadata.NewIncomingContext(t.Context(), metadata.Pairs("connection-name", "pg")),
		recv: []*pb.Packet{
			pkt(errResp, info("rule-1")),
			pkt(readyForQuery, info("rule-1")),
			pkt(readyForQuery, nil),
			pkt(errResp, info("rule-2")),
			pkt(readyForQuery, info("rule-2")),
			pkt(dataRow, nil),
			pkt(combined, info("rule-3")),
		},
	}
	agent := streamclient.NewAgent(models.Agent{ID: "agent-1", OrgID: orgID, Name: "agent-1"}, agentTransport)
	_ = (&Server{}).listenAgentMessages(pctx, agent)

	require.Len(t, proxyTransport.sent, 7, "every packet must reach the client")

	var raw []byte
	require.NoError(t, models.DB.Raw(`SELECT guardrails_info FROM private.sessions WHERE id = ?`, sid).Row().Scan(&raw))
	require.NotNil(t, raw, "guardrails_info is null: the block was not saved")
	var got []models.SessionGuardRailsInfo
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Len(t, got, 3, "one entry per block: %s", raw)
	require.Equal(t, "rule-1", got[0].RuleName)
	require.Equal(t, "rule-2", got[1].RuleName)
	require.Equal(t, "rule-3", got[2].RuleName)
}

// pgMsg encodes one Postgres backend message.
func pgMsg(typ pgtypes.PacketType, body []byte) []byte {
	msg := []byte{byte(typ), 0, 0, 0, 0}
	binary.BigEndian.PutUint32(msg[1:], uint32(4+len(body)))
	return append(msg, body...)
}

var (
	dataRow         = pgMsg(pgtypes.ServerDataRow, []byte{0, 1, 0, 0, 0, 1, '1'})
	commandComplete = pgMsg(pgtypes.ServerCommandComplete, []byte("SELECT 1\x00"))
	parseComplete   = pgMsg(pgtypes.ServerParseComplete, nil)
	bindComplete    = pgMsg(pgtypes.ServerBindComplete, nil)
	readyForQuery   = pgMsg(pgtypes.ServerReadyForQuery, []byte{'I'})
)

func TestHasPGErrorResponse(t *testing.T) {
	errResp := pgtypes.NewError("%s", "x").Encode()
	for name, tc := range map[string]struct {
		payload []byte
		first   bool
		any     bool
	}{
		"error response":                {errResp, true, true},
		"ready for query":               {readyForQuery, false, false},
		"empty":                         {nil, false, false},
		"error after data row":          {bytes.Join([][]byte{dataRow, commandComplete, errResp, readyForQuery}, nil), false, true},
		"error after row description":   {bytes.Join([][]byte{pgMsg(pgtypes.ServerRowDescription, []byte{0, 0}), errResp}, nil), false, true},
		"error after server-only types": {bytes.Join([][]byte{parseComplete, bindComplete, readyForQuery, errResp}, nil), false, true},
		"rows only":                     {bytes.Join([][]byte{dataRow, commandComplete, readyForQuery}, nil), false, false},
		"truncated":                     {dataRow[:6], false, false},
	} {
		t.Run(name, func(t *testing.T) {
			p := &pb.Packet{Payload: tc.payload}
			require.Equal(t, tc.first, isPGErrorResponsePacket(p))
			require.Equal(t, tc.any, hasPGErrorResponse(p))
		})
	}
}
