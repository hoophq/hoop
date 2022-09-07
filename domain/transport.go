package domain

type (
	Packet struct {
		Component  string
		PacketType PacketType
		Spec       map[string][]byte
		Payload    []byte
	}

	PacketType string
)
