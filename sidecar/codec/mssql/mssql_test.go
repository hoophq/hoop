// The MSSQL routing-redirect refusal, driven through a real Gate.
//
// A routing ENVCHANGE tells the client to reconnect somewhere else. Honored,
// it moves the session to a socket the relay does not hold, and the audit
// trail simply stops with no sign anything was wrong. The codec refuses it
// with ErrStreamUnsafe; these two cases assert the refusal survives the trip
// through gate.New and reaches a decision, and that the guard retires on both
// direction codecs once login completes.
//
// The wire-level cases for the same guard live with the codec, in
// libhoop/v2/codec/mssql. Only the ones needing a Gate are here.

package mssql_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/session"
)

func ucs2(s string) []byte {
	var b bytes.Buffer
	for _, r := range s {
		binary.Write(&b, binary.LittleEndian, uint16(r))
	}
	return b.Bytes()
}

// tdsPacket wraps a body in the 8-byte TDS header.
func tdsPacket(typ byte, eom bool, body []byte) []byte {
	var b bytes.Buffer
	b.WriteByte(typ)
	status := byte(0x00)
	if eom {
		status = 0x01
	}
	b.WriteByte(status)
	binary.Write(&b, binary.BigEndian, uint16(8+len(body)))
	b.Write([]byte{0, 0}) // SPID
	b.WriteByte(1)        // packet id
	b.WriteByte(0)        // window
	b.Write(body)
	return b.Bytes()
}

// sqlBatchBody builds a SQLBatch body: ALL_HEADERS then the UCS-2 statement.
func sqlBatchBody(sql string) []byte {
	var b bytes.Buffer
	const allHeadersLen = 22
	binary.Write(&b, binary.LittleEndian, uint32(allHeadersLen))
	b.Write(make([]byte, allHeadersLen-4))
	b.Write(ucs2(sql))
	return b.Bytes()
}

func sqlBatch(sql string) []byte {
	return tdsPacket(0x01, true, sqlBatchBody(sql))
}

// rpcExecuteSQL builds an RPCRequest calling sp_executesql with the statement
// as the first NVARCHAR parameter.
func routingEnvChange(server string, port uint16) []byte {
	name := ucs2(server)

	// RoutingData: Protocol(1) Port(2) AltServerLen(2) AltServer
	var routing bytes.Buffer
	routing.WriteByte(0) // TCP
	binary.Write(&routing, binary.LittleEndian, port)
	binary.Write(&routing, binary.LittleEndian, uint16(len(name)/2))
	routing.Write(name)

	// EnvValueData: Type(1) ValueLength(2) RoutingData OldValueLength(2)
	var value bytes.Buffer
	value.WriteByte(20) // routing
	binary.Write(&value, binary.LittleEndian, uint16(routing.Len()))
	value.Write(routing.Bytes())
	binary.Write(&value, binary.LittleEndian, uint16(0)) // OldValue

	var tok bytes.Buffer
	tok.WriteByte(0xe3)
	binary.Write(&tok, binary.LittleEndian, uint16(value.Len()))
	tok.Write(value.Bytes())
	return tok.Bytes()
}

// A Kerberos login is opaque to this codec by design: the AP-REQ lives in
// packet type 0x11, which carries no SQL and must reach the server byte for
// byte. If the decoder ever reported a statement here, a policy would be
// evaluating a service ticket as if it were text.
func loginAck() []byte {
	name := ucs2("Microsoft SQL Server")
	var b bytes.Buffer
	b.WriteByte(0xad)
	binary.Write(&b, binary.LittleEndian, uint16(10+len(name)))
	b.WriteByte(1)                          // Interface
	b.Write([]byte{0x04, 0x00, 0x00, 0x74}) // TDSVersion 7.4
	b.WriteByte(byte(len(name) / 2))        // ProgName length, in characters
	b.Write(name)
	b.Write([]byte{16, 0, 0x07, 0xd0}) // major, minor, build hi/lo
	return b.Bytes()
}

// loginResponse builds the server's reply to LOGIN7: LOGINACK, whatever the
// caller wants in between, and the terminating DONE.
func loginResponse(between ...[]byte) []byte {
	var body bytes.Buffer
	body.Write(loginAck())
	for _, b := range between {
		body.Write(b)
	}
	body.Write(doneToken())
	return tdsPacket(0x04, true, body.Bytes())
}

// Result-set bytes are attacker-influenced: a user can SELECT a string that
// happens to contain 0xE3. Scanning those would let any user with query
// access kill their own connection, so the guard retires once the login
// response has been seen — which is also the only place MS-TDS puts routing
// information.
//
// Retirement has to key off a SERVER-side observation. The gate builds one
// codec per direction, so a flag set while decoding the client's stream is
// written on an instance this one cannot reach.
func mssqlGate(t *testing.T) *gate.Gate {
	t.Helper()
	g, err := gate.New(
		session.New(inspect.MSSQL, session.Identity{Subject: "alice@example.com"}),
		gate.Config{Protocol: inspect.MSSQL},
	)
	if err != nil {
		t.Fatalf("gate.New: %v", err)
	}
	return g
}

// The regression test for the bug this guard shipped with.
//
// gate.New builds an Inspector per direction, so there are two Codec values
// and neither sees the other's fields. The guard originally retired on a flag
// set while decoding a CLIENT query, which the server-side codec never
// observes: the flag stayed false for the life of every connection, the scan
// never retired, and result rows were searched for redirect tokens forever.
//
// A codec-level test drove both directions through one Codec and passed,
// which is exactly why this one goes through gate.New instead.
func TestRoutingGuardRetiresAcrossDirectionCodecs(t *testing.T) {
	g := mssqlGate(t)
	ctx := context.Background()

	if d := g.Response(ctx, loginResponse()); !d.Allowed {
		t.Fatalf("clean login response denied: %s", d.Message)
	}
	if d := g.Request(ctx, sqlBatch("SELECT payload FROM blobs")); !d.Allowed {
		t.Fatalf("query denied: %s", d.Message)
	}

	// Row data whose bytes spell a routing ENVCHANGE. A user can put these
	// in a table and SELECT them.
	rows := tdsPacket(0x04, true, routingEnvChange("secondary.corp.example", 1433))
	if d := g.Response(ctx, rows); !d.Allowed {
		t.Fatalf("post-login result data was refused as a redirect: %s", d.Message)
	}
}

// And the guard still fires where it has to, down the same path.
func TestRoutingRedirectIsDeniedThroughTheGate(t *testing.T) {
	g := mssqlGate(t)

	redirect := loginResponse(routingEnvChange("secondary.corp.example", 1433))
	d := g.Response(context.Background(), redirect)

	if d.Allowed {
		t.Fatal("a routing redirect was forwarded to the client")
	}
	if d.Rule != "stream-unsafe" {
		t.Errorf("rule = %q, want stream-unsafe", d.Rule)
	}
	if !strings.Contains(d.Message, "secondary.corp.example:1433") {
		t.Errorf("message does not name the redirect target: %s", d.Message)
	}
}

// A stray 0xE3 with no valid structure behind it must not trip the guard.

// doneToken closes a server response batch.
func doneToken() []byte {
	b := []byte{0xfd}
	b = binary.LittleEndian.AppendUint16(b, 0)
	b = binary.LittleEndian.AppendUint16(b, 0)
	return binary.LittleEndian.AppendUint64(b, 0)
}
