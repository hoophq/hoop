package mongotypes

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/hoophq/hoop/common/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
	"go.mongodb.org/mongo-driver/x/mongo/driver/wiremessage"
)

type PacketHeader struct {
	MessageLength uint32
	RequestID     uint32
	ResponseTo    uint32
	OpCode        uint32
}

func (h *PacketHeader) Encode() []byte {
	pktBytes := make([]byte, binary.Size(h))
	binary.LittleEndian.PutUint32(pktBytes[0:4], h.MessageLength)
	binary.LittleEndian.PutUint32(pktBytes[4:8], h.RequestID)
	binary.LittleEndian.PutUint32(pktBytes[8:12], h.ResponseTo)
	binary.LittleEndian.PutUint32(pktBytes[12:16], h.OpCode)
	return pktBytes
}

type Packet struct {
	MessageLength uint32
	RequestID     uint32
	ResponseTo    uint32
	OpCode        uint32

	Frame []byte
}

func (p *Packet) Encode() []byte {
	pktBytes := make([]byte, len(p.Frame)+16)
	binary.LittleEndian.PutUint32(pktBytes[0:4], p.MessageLength)
	binary.LittleEndian.PutUint32(pktBytes[4:8], p.RequestID)
	binary.LittleEndian.PutUint32(pktBytes[8:12], p.ResponseTo)
	binary.LittleEndian.PutUint32(pktBytes[12:16], p.OpCode)
	_ = copy(pktBytes[16:], p.Frame)
	return pktBytes
}

func (p *Packet) Dump() { fmt.Println(hex.Dump(p.Encode())) }

func Decode(r io.Reader) (*Packet, error) {
	var header [16]byte
	_, err := io.ReadFull(r, header[:])
	if err != nil {
		return nil, err
	}

	p := Packet{
		MessageLength: binary.LittleEndian.Uint32(header[0:4]),
		RequestID:     binary.LittleEndian.Uint32(header[4:8]),
		ResponseTo:    binary.LittleEndian.Uint32(header[8:12]),
		OpCode:        binary.LittleEndian.Uint32(header[12:16]),
	}
	pktLen := int(p.MessageLength - 16)
	frame := make([]byte, pktLen)
	_, err = io.ReadFull(r, frame)
	if err != nil {
		return nil, err
	}
	p.Frame = frame
	return &p, nil
}

// DecodeOpMsgToJSON with return json content separated by break line for
// each document parsed in the packet
func DecodeOpMsgToJSON(pkt *Packet) ([]byte, error) {
	if pkt.OpCode != OpMsgType {
		return nil, nil
	}

	log.Debugf("decoding op msg to json, reqid=%v, respto=%v, msglength=%v, frame=%v",
		pkt.RequestID, pkt.ResponseTo, pkt.MessageLength, len(pkt.Frame))

	wm := make([]byte, len(pkt.Frame[4:]))
	_ = copy(wm, pkt.Frame[4:])
	var resultDocs []bsoncore.Document
	for i := 0; ; i++ {
		var stype wiremessage.SectionType
		var ok bool

		// stop processing when there's no more data
		if len(wm) == 0 {
			break
		}
		stype, wm, ok = wiremessage.ReadMsgSectionType(wm)
		if !ok {
			return nil, fmt.Errorf("failed decoding OP_MSG: unable to read section type")
		}
		switch stype {
		case wiremessage.DocumentSequence:
			var docs []bsoncore.Document
			_, docs, wm, ok = wiremessage.ReadMsgSectionDocumentSequence(wm)
			if !ok {
				return nil, fmt.Errorf("failed decoding OP_MSG: wiremessage is too short to unmarshal")
			}
			resultDocs = append(resultDocs, docs...)
		case wiremessage.SingleDocument:
			var doc bsoncore.Document
			doc, wm, ok = wiremessage.ReadMsgSectionSingleDocument(wm)
			if !ok {
				return nil, fmt.Errorf("failed decoding OP_MSG: wiremessage is too short to unmarshal")
			}
			resultDocs = append(resultDocs, doc)
		default:
			return nil, fmt.Errorf("failed decoding OP_MSG: found unknown section type (%v)", stype)
		}
	}

	var result []byte
	for _, doc := range resultDocs {
		data, err := bson.MarshalExtJSON(doc, false, false)
		if err != nil {
			return nil, fmt.Errorf("failed decoding OP_MSG: unable to re-encode document to json")
		}
		result = append(result, data...)
		result = append(result, '\n')
	}

	return result, nil
}
