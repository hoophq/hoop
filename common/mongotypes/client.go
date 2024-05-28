package mongotypes

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
)

type SaslOptions struct {
	SkipEmptyExchange bool `bson:"skipEmptyExchange"`
}

type SaslRequest struct {
	SaslStart int32       `bson:"saslStart"`
	Mechanism string      `bson:"mechanism"`
	Payload   []byte      `bson:"payload"`
	Database  string      `bson:"db"`
	Options   SaslOptions `bson:"options"`
}

type ClientApplication struct {
	Name string `bson:"name"`
}

type ClientDriver struct {
	Name    string `bson:"name"`
	Version string `bson:"version"`
}

type ClientOS struct {
	Type         string `bson:"type"`
	Name         string `bson:"name,omitempty"`
	Architecture string `bson:"architecture"`
	Version      string `bson:"version,omitempty"`
}

type ClientInfo struct {
	Application *ClientApplication `bson:"application,omitempty"`
	Driver      ClientDriver       `bson:"driver"`
	OS          ClientOS           `bson:"os"`
	Platform    string             `bson:"platform"`
}

type HelloCommand struct {
	IsMaster                int32        `bson:"ismaster"`
	HelloOK                 bool         `bson:"helloOk"`
	SaslSupportedMechs      *string      `bson:"saslSupportedMechs,omitempty"`
	SpeculativeAuthenticate *SaslRequest `bson:"speculativeAuthenticate,omitempty"`
	Compression             []any        `bson:"compression"`
	ClientInfo              ClientInfo   `bson:"client"`
}

func (c *ClientInfo) ApplicationName() string {
	if c.Application != nil {
		return c.Application.Name
	}
	return ""
}

func DecodeClientHelloCommand(reqPacket io.Reader) (*HelloCommand, error) {
	pkt, err := Decode(reqPacket)
	if err != nil {
		return nil, err
	}
	if pkt.OpCode != OpQueryType {
		return nil, fmt.Errorf("wrong type of packet %v", pkt.OpCode)
	}
	var resp HelloCommand
	if err := decodeOpQuery(pkt).UnmarshalBSON(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CopyAuthPayload returns a copy of the speculative authenticate payload
func (h *HelloCommand) CopyAuthPayload() []byte {
	authPayload := make([]byte, len(h.SpeculativeAuthenticate.Payload))
	_ = copy(authPayload, h.SpeculativeAuthenticate.Payload)
	return authPayload
}

func (h *HelloCommand) Encode(requestID, flagBits uint32) (*Packet, error) {
	helloCommandBytes, err := bson.Marshal(h)
	if err != nil {
		return nil, fmt.Errorf("failed encode hello command, err=%v", err)
	}
	fullCollectionName := "admin.$cmd"
	if h.SpeculativeAuthenticate != nil {
		fullCollectionName = fmt.Sprintf("%s.$cmd", h.SpeculativeAuthenticate.Database)
	}
	var header PacketHeader
	frameSize :=
		binary.Size(flagBits) +
			len(fullCollectionName) + 1 + // 0x00 delimiter
			4 + // number to skip
			4 + // number to return
			len(helloCommandBytes)
	frame := make([]byte, frameSize)

	binary.LittleEndian.PutUint32(frame[0:4], flagBits)
	idx := len(fullCollectionName) + 1 // 0x00 delimiter
	if copied := copy(frame[4:idx+4], fullCollectionName); copied != len(fullCollectionName) {
		return nil, fmt.Errorf("unable to copy full collection name, copied=%v/%v", copied, len(helloCommandBytes))
	}

	idx += 4 + 4 // numberToSkip + numberToReturn
	numberToReturn := binary.LittleEndian.Uint32([]byte{0xff, 0xff, 0xff, 0xff})
	binary.LittleEndian.PutUint32(frame[idx:idx+4], numberToReturn)
	idx += 4
	if copied := copy(frame[idx:], helloCommandBytes); copied != len(helloCommandBytes) {
		return nil, fmt.Errorf("unable to copy full frame, copied=%v/%v", copied, len(helloCommandBytes))
	}

	return &Packet{
		MessageLength: uint32(binary.Size(header) + frameSize),
		RequestID:     requestID,
		OpCode:        OpQueryType,
		Frame:         frame,
	}, nil
}

type OpQuery struct {
	PacketHeader

	FlagBits           uint32
	FullCollectionName string
	NumberToSkip       uint32
	NumberToReturn     uint32
	Query              []byte
}

func decodeOpQuery(pkt *Packet) *OpQuery {
	m := OpQuery{
		PacketHeader: PacketHeader{
			MessageLength: pkt.MessageLength,
			RequestID:     pkt.RequestID,
			ResponseTo:    pkt.ResponseTo,
			OpCode:        pkt.OpCode,
		},
		FlagBits: binary.LittleEndian.Uint32(pkt.Frame[0:4]),
	}

	frame := pkt.Frame[4:]
	idx := bytes.IndexByte(frame, 0x00)
	if idx == -1 {
		return &m
	}
	m.FullCollectionName = string(frame[:idx])

	frame = frame[idx+1:] // +1 skip 0x00
	m.NumberToSkip = binary.LittleEndian.Uint32(frame[0:4])
	m.NumberToReturn = binary.LittleEndian.Uint32(frame[4:8])

	frame = frame[8:]
	m.Query = make([]byte, len(frame))
	_ = copy(m.Query, frame)
	return &m
}

func (o *OpQuery) UnmarshalBSON(v any) error {
	if len(o.Query) == 0 {
		return fmt.Errorf("not enough bytes to unmarshal document")
	}
	bodyDocument := bytes.NewBuffer(o.Query) // skip kind
	bodyRaw, err := bson.ReadDocument(bodyDocument)
	if err != nil {
		return err
	}
	// TODO: check if v is a pointer
	return bson.Unmarshal(bodyRaw, v)
}

type saslContinueRequest struct {
	SaslContinue   int32  `bson:"saslContinue"`
	ConversationID int32  `bson:"conversationId"`
	Payload        []byte `bson:"payload"`
	Database       string `bson:"$db"`
}

func DecodeSASLContinueRequest(pkt *Packet) ([]byte, error) {
	var req saslContinueRequest
	// skip flag bits (4) and document kind body (0)
	err := bson.Unmarshal(pkt.Frame[5:], &req)
	if err != nil {
		return nil, fmt.Errorf("failed decoding SASL continue packet, reason=%v", err)
	}
	if len(req.Payload) == 0 {
		return nil, fmt.Errorf("failed decoding (empty) SAS continue payload")
	}
	return req.Payload, nil
}

func NewSaslContinuePacket(requestID uint32, cid int32, payload []byte, dbName string) *Packet {
	doc := bsoncore.BuildDocumentFromElements(nil,
		bsoncore.AppendInt32Element(nil, "saslContinue", 1),
		bsoncore.AppendInt32Element(nil, "conversationId", cid),
		bsoncore.AppendBinaryElement(nil, "payload", 0x00, payload),
		bsoncore.AppendStringElement(nil, "$db", dbName),
	)
	frameSize := 4 + // message flags
		len(doc) +
		1 // document kind body
	frame := make([]byte, frameSize)

	_ = copy(frame[5:], doc)
	return &Packet{
		MessageLength: uint32(frameSize + 16),
		RequestID:     requestID,
		OpCode:        OpMsgType,
		Frame:         frame,
	}
}
