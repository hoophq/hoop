package audit

import (
	"bytes"
	"fmt"

	"github.com/hoophq/hoop/common/mongotypes"
)

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
