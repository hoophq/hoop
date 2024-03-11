package mongotypes

import (
	"bytes"
	"encoding/binary"

	"go.mongodb.org/mongo-driver/bson"
)

type OpMsg struct {
	PacketHeader

	FlagBits    uint32
	SectionBody []byte
}

func DecodeOpMsg(pkt *Packet) *OpMsg {
	if pkt.OpCode != 2013 {
		return nil
	}
	m := OpMsg{
		PacketHeader: PacketHeader{
			MessageLength: pkt.MessageLength,
			RequestID:     pkt.RequestID,
			ResponseTo:    pkt.ResponseTo,
			OpCode:        pkt.OpCode,
		},
		FlagBits: binary.LittleEndian.Uint32(pkt.Frame[0:4]),
	}
	m.SectionBody = make([]byte, len(pkt.Frame[4:]))
	_ = copy(m.SectionBody, pkt.Frame[4:])
	// TODO: need to be able to parse more kinds (e.g.: 1, 2)
	if m.SectionBody[0] != 0x00 {
		return nil
	}
	return &m
}

func (o *OpMsg) DecodeInto(v any) error {
	// rawDoc, err := bson.ReadDocument(bytes.NewBuffer(dataBytes))
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// out := map[string]any{}
	// bson.Unmarshal(rawDoc, &out)

	bodyDocument := bytes.NewBuffer(o.SectionBody[1:]) // skip kind
	bodyRaw, err := bson.ReadDocument(bodyDocument)
	if err != nil {
		return err
	}
	// TODO: check if v is a pointer
	return bson.Unmarshal(bodyRaw, v)
}
