//go:build integration

package testutil

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func BuildPGEnvVars(host, port, user, pass, dbname, sslmode string) map[string]any {
	return map[string]any{
		"envvar:HOST":    b64(host),
		"envvar:USER":    b64(user),
		"envvar:PASS":    b64(pass),
		"envvar:PORT":    b64(port),
		"envvar:DB":      b64(dbname),
		"envvar:SSLMODE": b64(sslmode),
	}
}

func BuildSessionOpenPacket(sessionID, connType string, envVars map[string]any) *pb.Packet {
	connParams := &pb.AgentConnectionParams{
		ConnectionName: "test-connection",
		ConnectionType: connType,
		UserID:         "test-user-id",
		UserEmail:      "test@example.com",
		EnvVars:        envVars,
		CmdList:        nil,
		ClientArgs:     nil,
		ClientVerb:     "connect",
		ClientOrigin:   "client",
	}

	encParams, err := pb.GobEncode(connParams)
	if err != nil {
		panic(fmt.Sprintf("failed to gob-encode connection params: %v", err))
	}

	return &pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:         []byte(sessionID),
			pb.SpecConnectionType:           []byte(connType),
			pb.SpecAgentConnectionParamsKey: encParams,
		},
	}
}

func BuildSessionClosePacket(sessionID string, exitCode string) *pb.Packet {
	return &pb.Packet{
		Type: pbagent.SessionClose,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:  []byte(sessionID),
			pb.SpecClientExitCodeKey: []byte(exitCode),
		},
	}
}

func OpenPGSession(t T, tr *MockTransport, host, port, user, pass, dbname string) string {
	sessionID := uuid.New().String()
	envVars := BuildPGEnvVars(host, port, user, pass, dbname, "disable")
	pkt := BuildSessionOpenPacket(sessionID, string(pb.ConnectionTypePostgres), envVars)

	tr.Inject(pkt)

	timeout := 10 * time.Second
	deadline := time.After(timeout)
	for {
		select {
		case resp := <-tr.RecvCh():
			switch resp.Type {
			case pbclient.SessionOpenOK:
				return sessionID
			case pbclient.SessionClose:
				t.Fatalf("session open failed, agent sent SessionClose: %s", string(resp.Payload))
			default:
				continue
			}
		case <-deadline:
			t.Fatalf("timed out waiting for SessionOpenOK after %v", timeout)
		}
	}
}

func SendPGWrite(t T, tr *MockTransport, sessionID, connID string, payload []byte) {
	pkt := &pb.Packet{
		Type: pbagent.PGConnectionWrite,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sessionID),
			pb.SpecClientConnectionID: []byte(connID),
		},
		Payload: payload,
	}
	tr.Inject(pkt)
}

func WaitForPGReady(t T, tr *MockTransport, timeout time.Duration) {
	deadline := time.After(timeout)
	for {
		select {
		case resp := <-tr.RecvCh():
			switch resp.Type {
			case pbclient.PGConnectionWrite:
				msgs := ParsePGMessages(resp.Payload)
				for _, msg := range msgs {
					if msg.Type == byte('Z') {
						return
					}
				}
			case pbclient.SessionClose:
				t.Fatalf("connection failed during PG handshake: %s", string(resp.Payload))
			default:
				continue
			}
		case <-deadline:
			t.Fatalf("timed out waiting for PG ReadyForQuery after %v", timeout)
		}
	}
}

func CollectPGResponses(t T, tr *MockTransport, timeout time.Duration) []*pb.Packet {
	var pkts []*pb.Packet
	deadline := time.After(timeout)
outer:
	for {
		select {
		case resp, ok := <-tr.RecvCh():
			if !ok {
				break outer
			}
			if resp.Type != pbclient.PGConnectionWrite {
				if resp.Type == pbclient.SessionClose {
					t.Fatalf("received SessionClose while collecting PG responses: %s", string(resp.Payload))
				}
				continue
			}
			pkts = append(pkts, resp)
			msgs := ParsePGMessages(resp.Payload)
			for _, msg := range msgs {
				if msg.Type == byte('Z') {
					return pkts
				}
			}
		case <-deadline:
			t.Fatalf("timed out collecting PG responses after %v, got %d packets", timeout, len(pkts))
		}
	}
	return pkts
}

func ExtractQueryResult(pkts []*pb.Packet) (commandTag string, columns []string, rows [][]string) {
	for _, pkt := range pkts {
		msgs := ParsePGMessages(pkt.Payload)
		for _, msg := range msgs {
			switch msg.Type {
			case byte('T'):
				columns = msg.AsRowDescription()
			case byte('D'):
				rowVals := msg.AsDataRow()
				var row []string
				for _, v := range rowVals {
					row = append(row, string(v))
				}
				rows = append(rows, row)
			case byte('C'):
				commandTag = msg.AsCommandComplete()
			}
		}
	}
	return commandTag, columns, rows
}

func ExtractAllQueryResults(pkts []*pb.Packet) []QueryResult {
	var results []QueryResult
	var currentCmdTag string
	var currentCols []string
	var currentRows [][]string
	seenRowDesc := false

	for _, pkt := range pkts {
		msgs := ParsePGMessages(pkt.Payload)
		for _, msg := range msgs {
			switch msg.Type {
			case byte('T'):
				currentCols = nil
				currentRows = nil
				currentCmdTag = ""
				seenRowDesc = true
				for _, col := range msg.AsRowDescription() {
					currentCols = append(currentCols, col)
				}
			case byte('D'):
				rowVals := msg.AsDataRow()
				var row []string
				for _, v := range rowVals {
					row = append(row, string(v))
				}
				currentRows = append(currentRows, row)
			case byte('C'):
				currentCmdTag = msg.AsCommandComplete()
			case byte('Z'):
				if seenRowDesc {
					results = append(results, QueryResult{
						CommandTag: currentCmdTag,
						Columns:    currentCols,
						Rows:       currentRows,
					})
				}
				currentCols = nil
				currentRows = nil
				currentCmdTag = ""
				seenRowDesc = false
			}
		}
	}
	return results
}

type QueryResult struct {
	CommandTag string
	Columns    []string
	Rows       [][]string
}

func (qr QueryResult) RowCount() int {
	if qr.Columns == nil {
		return 0
	}
	return len(qr.Rows) - 1
}

func (qr QueryResult) Row(i int) []string {
	if qr.Columns == nil || i+1 >= len(qr.Rows) {
		return nil
	}
	return qr.Rows[i+1]
}

func (qr QueryResult) HasError() bool {
	for _, row := range qr.Rows {
		if len(row) > 0 && strings.HasPrefix(row[0], "ERROR") {
			return true
		}
	}
	return false
}
