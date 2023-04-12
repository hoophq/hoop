package types

// https://dev.mysql.com/doc/internals/en/capability-flags.html
type ClientFlag uint32

const (
	// Upper
	ClientLongPassword ClientFlag = 1 << iota
	ClientFoundRows
	ClientLongFlag
	ClientConnectWithDB
	ClientNoSchema
	ClientCompress
	ClientODBC
	ClientLocalFiles
	ClientIgnoreSpace
	ClientProtocol41
	ClientInteractive
	ClientSSL
	ClientIgnoreSIGPIPE
	ClientTransactions
	ClientReserved
	ClientSecureConn

	// Lower
	ClientMultiStatements
	ClientMultiResults
	ClientPSMultiResults
	ClientPluginAuth
	ClientConnectAttrs
	ClientPluginAuthLenEncClientData
	ClientCanHandleExpiredPasswords
	ClientSessionTrack
	ClientDeprecateEOF
)

var flagsMap = map[ClientFlag]string{
	ClientLongPassword:               "clientLongPassword",
	ClientFoundRows:                  "clientFoundRows",
	ClientLongFlag:                   "clientLongFlag",
	ClientConnectWithDB:              "clientConnectWithDB",
	ClientNoSchema:                   "clientNoSchema",
	ClientCompress:                   "clientCompress",
	ClientODBC:                       "clientODBC",
	ClientLocalFiles:                 "clientLocalFiles",
	ClientIgnoreSpace:                "clientIgnoreSpace",
	ClientProtocol41:                 "clientProtocol41",
	ClientInteractive:                "clientInteractive",
	ClientSSL:                        "clientSSL",
	ClientIgnoreSIGPIPE:              "clientIgnoreSIGPIPE",
	ClientTransactions:               "clientTransactions",
	ClientReserved:                   "clientReserved",
	ClientSecureConn:                 "clientSecureConn",
	ClientMultiStatements:            "clientMultiStatements",
	ClientMultiResults:               "clientMultiResults",
	ClientPSMultiResults:             "clientPSMultiResults",
	ClientPluginAuth:                 "clientPluginAuth",
	ClientConnectAttrs:               "clientConnectAttrs",
	ClientPluginAuthLenEncClientData: "clientPluginAuthLenEncClientData",
	ClientCanHandleExpiredPasswords:  "clientCanHandleExpiredPasswords",
	ClientSessionTrack:               "clientSessionTrack",
	ClientDeprecateEOF:               "clientDeprecateEOF",
}
