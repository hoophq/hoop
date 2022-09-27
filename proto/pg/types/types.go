package types

type PacketType byte

func (t PacketType) Byte() byte {
	return byte(t)
}

// http://www.postgresql.org/docs/9.4/static/protocol-message-formats.html
const (
	ClientBind        PacketType = 'B'
	ClientClose       PacketType = 'C'
	ClientCopyData    PacketType = 'd'
	ClientCopyDone    PacketType = 'c'
	ClientCopyFail    PacketType = 'f'
	ClientDescribe    PacketType = 'D'
	ClientExecute     PacketType = 'E'
	ClientFlush       PacketType = 'H'
	ClientParse       PacketType = 'P'
	ClientPassword    PacketType = 'p'
	ClientSimpleQuery PacketType = 'Q'
	ClientSync        PacketType = 'S'
	ClientTerminate   PacketType = 'X'

	ServerAuth                 PacketType = 'R'
	ServerBindComplete         PacketType = '2'
	ServerCommandComplete      PacketType = 'C'
	ServerCloseComplete        PacketType = '3'
	ServerCopyInResponse       PacketType = 'G'
	ServerDataRow              PacketType = 'D'
	ServerEmptyQuery           PacketType = 'I'
	ServerErrorResponse        PacketType = 'E'
	ServerNoticeResponse       PacketType = 'N'
	ServerNoData               PacketType = 'n'
	ServerParameterDescription PacketType = 't'
	ServerParameterStatus      PacketType = 'S'
	ServerParseComplete        PacketType = '1'
	ServerPortalSuspended      PacketType = 's'
	ServerBackendKeyData       PacketType = 'K'
	ServerReadyForQuery        PacketType = 'Z'
	ServerRowDescription       PacketType = 'T'
	ServerSSLNotSupported      PacketType = 'N'
)

const (
	ClientSSLRequestMessage    uint32 = 80877103
	ClientGSSENCRequestMessage uint32 = 80877104
)

type AuthPacketType int

const (
	ServerAuthenticationSASL              AuthPacketType = 10
	ServerAuthenticationSASLContinue      AuthPacketType = 11
	ServerAuthenticationSASLFinal         AuthPacketType = 12
	ServerAuthenticationMD5Password       AuthPacketType = 5
	ServerAuthenticationCleartextPassword AuthPacketType = 3
	ServerAuthenticationOK                AuthPacketType = 0
)
