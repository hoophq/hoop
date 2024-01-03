package mssqltypes

import (
	"fmt"
)

// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-tds/f2026cd3-9a46-4a3f-9a08-f63140bcbbe3
// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-tds/e17e54ae-0fac-48b7-b8a8-c267be297923
func DecodeSQLBatchToRawQuery(data []byte) (string, error) {
	// pkt header + sql batch header length
	if len(data) < 12 {
		return "", fmt.Errorf("not a valid sql batch type, data=%X", data)
	}
	if PacketType(data[0]) != PacketSQLBatchType {
		return "", fmt.Errorf("it's not a sql batch type, found=%X", data[0])
	}
	// re slice after packet header
	data = data[8:]
	if data[6] == 0x01 {
		// skip ALL_HEADERS
		return ucs22str(data[22:]), nil
	}
	return ucs22str(data), nil
}
