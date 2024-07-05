package pgtypes

const DefaultBufferSize = 1 << 24

type PacketType byte

func (t PacketType) Byte() byte { return byte(t) }
func (t PacketType) String() string {
	if s, ok := clientPacketType[t]; ok {
		return s
	}
	return "unknown"
}

// client
// http://www.postgresql.org/docs/9.4/static/protocol-message-formats.html
const (
	ClientBind           PacketType = 'B'
	ClientClose          PacketType = 'C'
	ClientCopyData       PacketType = 'd'
	ClientCopyDone       PacketType = 'c'
	ClientCopyFail       PacketType = 'f'
	ClientDescribe       PacketType = 'D'
	ClientExecute        PacketType = 'E'
	ClientFlush          PacketType = 'H'
	ClientParse          PacketType = 'P'
	ClientPassword       PacketType = 'p'
	ClientSimpleQuery    PacketType = 'Q'
	ClientSync           PacketType = 'S'
	ClientTerminate      PacketType = 'X'
	ServerBackendKeyData PacketType = 'K'
)

const ClientCancelRequestMessage uint32 = 80877102

var clientPacketType = map[PacketType]string{
	ClientBind:        "ClientBind",
	ClientClose:       "ClientClose",
	ClientCopyData:    "ClientCopyData",
	ClientCopyDone:    "ClientCopyDone",
	ClientCopyFail:    "ClientCopyFail",
	ClientDescribe:    "ClientDescribe",
	ClientExecute:     "ClientExecute",
	ClientFlush:       "ClientFlush",
	ClientParse:       "ClientParse",
	ClientPassword:    "ClientPassword",
	ClientSimpleQuery: "ClientSimpleQuery",
	ClientSync:        "ClientSync",
	ClientTerminate:   "ClientTerminate",
}

func isClientType(packetType byte) (isClient bool) {
	_, isClient = clientPacketType[PacketType(packetType)]
	return
}
