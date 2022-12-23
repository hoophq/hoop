package pg

type (
	contextKey int
)

const (
	DefaultBufferSize              = 1 << 24 // 16777216 bytes
	SessionIDContextKey contextKey = iota
)

type (
	PacketType     byte
	AuthPacketType int
)

func (t PacketType) Byte() byte {
	return byte(t)
}

// client
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
	ClientCancelRequestMessage uint32 = 80877102
)

const (
	ServerAuthenticationSASL              AuthPacketType = 10
	ServerAuthenticationSASLContinue      AuthPacketType = 11
	ServerAuthenticationSASLFinal         AuthPacketType = 12
	ServerAuthenticationMD5Password       AuthPacketType = 5
	ServerAuthenticationCleartextPassword AuthPacketType = 3
	ServerAuthenticationOK                AuthPacketType = 0
	ServerDataRowNull                     uint32         = 4294967295 // FF FF FF FF
)

// server
type (
	Severity string
	Code     string
)

const (
	LevelError   Severity = "ERROR"
	LevelFatal   Severity = "FATAL"
	LevelPanic   Severity = "PANIC"
	LevelWarning Severity = "WARNING"
	LevelNotice  Severity = "NOTICE"
	LevelDebug   Severity = "DEBUG"
	LevelInfo    Severity = "INFO"
	LevelLog     Severity = "LOG"
)

// Possible values are 'I' if idle (not in a transaction block); 'T' if in a
// transaction block; or 'E' if in a failed transaction block
// (queries will be rejected until block is ended).
const (
	ServerIdle              = 'I'
	ServerTransactionBlock  = 'T'
	ServerTransactionFailed = 'E'
)

const (
	// Class 08 - Connection Exception
	ConnectionFailure Code = "08006"
	// Class 0A — Feature Not Supported
	FeatureNotSupported Code = "0A000"
	// Class 28 — Invalid Authorization Specification
	InvalidPassword                   Code = "28P01"
	InvalidAuthorizationSpecification Code = "28000"
)
