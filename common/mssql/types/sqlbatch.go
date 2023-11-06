package mssqltypes

import (
	"encoding/binary"
	"fmt"
)

// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-tds/f2026cd3-9a46-4a3f-9a08-f63140bcbbe3
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
	batchHeaderLength := binary.LittleEndian.Uint32(data[:4])
	if int(batchHeaderLength) > len(data) {
		return "", fmt.Errorf("sql batch header length (%v) is greater than the whole packet (%v)",
			batchHeaderLength, len(data))
	}
	return ucs22str(data[batchHeaderLength:]), nil
}
