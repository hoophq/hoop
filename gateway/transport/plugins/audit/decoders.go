package audit

import (
	"bytes"
	"fmt"

	oracletypes "libhoop/agent/oracle/types"

	"github.com/hoophq/hoop/common/mongotypes"
)

// minOracleTextRunLen is the shortest printable-ASCII run extracted from an
// Oracle TTC payload. It mirrors the threshold libhoop uses for Oracle input
// guardrails and metrics, keeping the audit log consistent with what those
// subsystems already scan.
const minOracleTextRunLen = 4

// decodeOracleClientQuery extracts printable text (SQL statements and other
// text fields) from a raw Oracle client (TNS) payload. Oracle has no
// gateway-side wire decoder, so - like the input guardrails and metrics
// analyzer in libhoop - this relies on printable-run extraction rather than
// precise OPI/SQL parsing. Returns nil when the payload carries no text (e.g.
// binary handshake/auth packets), so those frames produce no audit entry.
func decodeOracleClientQuery(payload []byte) []byte {
	text := oracletypes.ExtractText(payload, minOracleTextRunLen)
	if text == "" {
		return nil
	}
	return []byte(text)
}

// decodeMySQLCommandQuery try to decode a packet to see if it's a COMM_QUERY type
// https://dev.mysql.com/doc/dev/mysql-server/latest/page_protocol_com_query.html
func decodeMySQLCommandQuery(payload []byte) []byte {
	if len(payload) < 5 {
		return nil
	}
	// type packet
	pos := 4

	if payload[pos] != 0x03 {
		return nil
	}

	if payload[pos+1] == 0x00 {
		// param count + param set count
		pos += 2
	}
	if len(payload) < pos {
		return nil
	}
	// TODO: must check when parameters is set
	return payload[pos:]
}

func decodeClientMongoOpMsgPacket(payload []byte) ([]byte, error) {
	pkt, err := mongotypes.Decode(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed decoding mongodb packet: %v", err)
	}
	switch pkt.OpCode {
	case mongotypes.OpCompressed:
		return nil, fmt.Errorf("compression is not supported")
	case mongotypes.OpMsgType:
		return mongotypes.DecodeOpMsgToJSON(pkt)
	}
	return nil, nil
}
