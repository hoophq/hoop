package mssqltypes

// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-tds/619c43b6-9495-4a58-9e49-a4950db245b3
// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-tds/cbe9c510-eae6-4b1f-9893-a098944d430a
// DecodeRpcRequestToRawQuery is a best effort to decode rpc requests packets where the proc id is Sp_ExecuteSql
// with TYPE_INFO is NVARCHARTYPE.
// Only the first parameter is decoded that should contain the main instructions to execute a query.
// func DecodeRpcRequestToRawQuery(v []byte) (string, error) {
// 	// pkt header + rpc request header length
// 	// var data []byte
// 	data := make([]byte, len(v))
// 	_ = copy(data, v)
// 	if len(data) < 12 {
// 		return "", fmt.Errorf("not a valid rpc request type, data=%X", data)
// 	}
// 	if PacketType(data[0]) != PacketRPCRequestType {
// 		return "", fmt.Errorf("it's not a sql batch type, found=%X", data[0])
// 	}
// 	// re slice after packet header
// 	data = data[8:]
// 	rpcRequestHeaderLength := binary.LittleEndian.Uint32(data[:4])
// 	if int(rpcRequestHeaderLength) > len(data) {
// 		return "", fmt.Errorf("rpc request header length (%v) is greater than the whole packet (%v)",
// 			rpcRequestHeaderLength, len(data))
// 	}
// 	data = data[rpcRequestHeaderLength:]
// 	if data[0] == 0xff && data[1] == 0xff {
// 		// it should be able to move up into the first parameter
// 		if len(data) < 18 {
// 			return "", nil
// 		}
// 		procID := data[2]
// 		if procID != sp_ExecuteSql {
// 			return "", nil
// 		}
// 		// proc name length + proc id + option flag + param name length + param status flag
// 		pos := 2 + 2 + 2 + 1 + 1
// 		data = data[pos:]
// 		typeInfo := data[0]
// 		if typeInfo != typeNVarChar {
// 			return "", nil
// 		}
// 		// move from type info
// 		data = data[8:]
// 		dataLength := binary.LittleEndian.Uint16(data[0:2])
// 		if int(dataLength) > len(data) {
// 			fmt.Println(hex.Dump(data))
// 			return "", fmt.Errorf("failed decoding rpc request, type data length (%v) is greater than the packet itself (%v)",
// 				dataLength, len(data))
// 		}
// 		return ucs22str(data[2:dataLength]), nil
// 	}

// 	return "", nil
// }
