package audit

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
