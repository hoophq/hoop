//go:build integration

package transport

import (
	"bytes"
	"encoding/binary"

	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
)

// Minimal PostgreSQL v3 wire-protocol helpers, sufficient to drive one
// authenticated query through the agent's transparent PG proxy. The proxy
// terminates the client-side auth and authenticates upstream itself using the
// connection's stored credentials, so the client here never performs SCRAM —
// it sends an SSLRequest + StartupMessage and then simple queries.
//
// These are intentionally duplicated (not imported from agent/integration)
// to keep this suite self-contained and free of the agent suite's container
// dependencies; the wire format is stable and tiny.
const (
	pgProtocolVersion3 = 196608
	pgProtocolSSL      = 80877103
)

func pgSSLRequest() []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint32(buf[0:4], 8)
	binary.BigEndian.PutUint32(buf[4:8], pgProtocolSSL)
	return buf
}

func pgStartupMessage(user, database string) []byte {
	var params bytes.Buffer
	params.WriteString("user")
	params.WriteByte(0)
	params.WriteString(user)
	params.WriteByte(0)
	params.WriteString("database")
	params.WriteByte(0)
	params.WriteString(database)
	params.WriteByte(0)
	params.WriteByte(0) // terminator

	length := 4 + 4 + params.Len()
	buf := make([]byte, length)
	binary.BigEndian.PutUint32(buf[0:4], uint32(length))
	binary.BigEndian.PutUint32(buf[4:8], pgProtocolVersion3)
	copy(buf[8:], params.Bytes())
	return buf
}

func pgSimpleQuery(sql string) []byte {
	q := append([]byte(sql), 0)
	total := 4 + len(q)
	buf := make([]byte, 1+total)
	buf[0] = 'Q'
	binary.BigEndian.PutUint32(buf[1:5], uint32(total))
	copy(buf[5:], q)
	return buf
}

type pgMessage struct {
	Type    byte
	Payload []byte
}

// parsePGMessages splits a payload into tagged PG messages. A lone SSL
// negotiation reply ('N'/'S', 1 byte) parses to zero messages because there
// is no length prefix behind it, which is exactly what callers want: they
// scan for 'Z'/'D' and ignore the negotiation byte.
func parsePGMessages(data []byte) []pgMessage {
	var msgs []pgMessage
	r := bytes.NewReader(data)
	for r.Len() > 0 {
		typ, err := r.ReadByte()
		if err != nil {
			break
		}
		if r.Len() < 4 {
			break
		}
		var lenBuf [4]byte
		if _, err := r.Read(lenBuf[:]); err != nil {
			break
		}
		frameLen := int(binary.BigEndian.Uint32(lenBuf[:])) - 4
		if frameLen < 0 || r.Len() < frameLen {
			break
		}
		frame := make([]byte, frameLen)
		if _, err := r.Read(frame); err != nil {
			break
		}
		msgs = append(msgs, pgMessage{Type: typ, Payload: frame})
	}
	return msgs
}

// dataRowValues decodes a DataRow ('D') message into its column values.
func dataRowValues(m pgMessage) [][]byte {
	if m.Type != 'D' {
		return nil
	}
	r := bytes.NewReader(m.Payload)
	var numCols int16
	if err := binary.Read(r, binary.BigEndian, &numCols); err != nil {
		return nil
	}
	vals := make([][]byte, 0, numCols)
	for i := 0; i < int(numCols); i++ {
		var colLen int32
		if err := binary.Read(r, binary.BigEndian, &colLen); err != nil {
			break
		}
		if colLen == -1 {
			vals = append(vals, nil)
			continue
		}
		val := make([]byte, colLen)
		if _, err := r.Read(val); err != nil {
			break
		}
		vals = append(vals, val)
	}
	return vals
}

// pgWritePacket wraps raw PG bytes in the agent-bound PGConnectionWrite packet,
// carrying the session id and a client connection id the way a real client
// proxy does.
func pgWritePacket(sid, connID string, payload []byte) *pb.Packet {
	return &pb.Packet{
		Type:    pbagent.PGConnectionWrite,
		Payload: payload,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sid),
			pb.SpecClientConnectionID: []byte(connID),
		},
	}
}
