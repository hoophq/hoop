package mssqltypes

type PacketType byte

const DefaultPacketSize = 4096

// packet types
// https://msdn.microsoft.com/en-us/library/dd304214.aspx
const (
	PacketSQLBatchType   PacketType = 0x01
	PacketRPCRequestType PacketType = 0x03
	PacketReplyType      PacketType = 0x04

	// 2.2.1.7 Attention: https://msdn.microsoft.com/en-us/library/dd341449.aspx
	// 4.19.2 Out-of-Band Attention Signal: https://msdn.microsoft.com/en-us/library/dd305167.aspx
	PacketAttentionType PacketType = 0x06

	PacketBulkLoadBCPType  PacketType = 0x07
	PacketFedAuthTokenType PacketType = 0x0a
	PacketTransMgrReqType  PacketType = 0x0e
	PacketNormalType       PacketType = 0x0f
	PacketLogin7Type       PacketType = 0x10
	PacketSSPIMessageType  PacketType = 0x11
	PacketPreloginType     PacketType = 0x12
)

var packetTypeMap = map[PacketType]string{
	PacketSQLBatchType:     "PacketSQLBatchType",
	PacketRPCRequestType:   "PacketRPCRequestType",
	PacketReplyType:        "PacketReplyType",
	PacketAttentionType:    "PacketAttentionType",
	PacketBulkLoadBCPType:  "PacketBulkLoadBCPType",
	PacketFedAuthTokenType: "PacketFedAuthTokenType",
	PacketTransMgrReqType:  "PacketTransMgrReqType",
	PacketNormalType:       "PacketNormalType",
	PacketLogin7Type:       "PacketLogin7Type",
	PacketSSPIMessageType:  "PacketSSPIMessageType",
	PacketPreloginType:     "PacketPreloginType",
}

const sp_ExecuteSql byte = 0x0a

// variable-length data types
// http://msdn.microsoft.com/en-us/library/dd358341.aspx
const typeNVarChar = 0xe7
