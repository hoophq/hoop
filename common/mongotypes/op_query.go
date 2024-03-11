package mongotypes

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"go.mongodb.org/mongo-driver/bson"
)

type OpQuery struct {
	PacketHeader

	FlagBits           uint32
	FullCollectionName string
	NumberToSkip       uint32
	NumberToReturn     uint32
	Query              []byte
}

func DecodeOpQuery(pkt *Packet) *OpQuery {
	if pkt.OpCode != OpQueryType {
		return nil
	}
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

func (o *OpQuery) Encode(v map[string]any) ([]byte, error) {
	return bson.Marshal(v)
}

func (o *OpQuery) EncodeFull(v map[string]any) ([]byte, error) {
	docBytes, err := bson.Marshal(v)
	if err != nil {
		return nil, err
	}
	fullCollectionName := fmt.Sprintf("%s.$cmd", o.FullCollectionName)

	frameSize := 4 + 4 + 4 + // flag bits + numbers to skip + number to return
		len(fullCollectionName) + 1 + // 0x00 delimiter
		len(docBytes)
	frame := make([]byte, frameSize)

	binary.LittleEndian.PutUint32(frame[0:4], o.FlagBits)

	idx := len(fullCollectionName) + 1 // 0x00 delimiter
	_ = copy(frame[4:idx], fullCollectionName)

	idx += 4
	binary.LittleEndian.PutUint32(frame[idx:idx+4], o.NumberToSkip)
	idx += 4
	binary.LittleEndian.PutUint32(frame[idx:idx+4], o.NumberToReturn)
	idx += 4
	_ = copy(frame[idx:], docBytes)

	fmt.Println("FRAMESIZE", len(frame))

	var header [16]byte
	binary.LittleEndian.PutUint32(header[0:4], uint32(len(frame)+16))
	binary.LittleEndian.PutUint32(header[4:8], o.RequestID)
	binary.LittleEndian.PutUint32(header[8:12], o.ResponseTo)
	binary.LittleEndian.PutUint32(header[12:16], o.OpCode)
	return append(header[:], frame...), nil
}

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
	IsMaster                int32       `bson:"isMaster"`
	HelloOK                 bool        `bson:"helloOk"`
	SpeculativeAuthenticate SaslRequest `bson:"speculativeAuthenticate"`
	Compression             []any       `bson:"compression"`
	ClientInfo              ClientInfo  `bson:"client"`
}

// IsValid validates if it's a valid hello command request
// https://github.com/mongodb/specifications/blob/v1/source/mongodb-handshake/handshake.rst#hello-command
func (a *HelloCommand) IsValid() (valid bool) {
	if a.ClientInfo.Application != nil {
		if a.ClientInfo.Application.Name == "" {
			return false
		}
	}
	return a.IsMaster == 1 &&
		a.HelloOK &&
		a.ClientInfo.Driver.Name != "" &&
		a.ClientInfo.Driver.Version != "" &&
		a.ClientInfo.OS.Type != ""
}

func DecodeClientHelloCommand(reqPacket io.Reader) (*HelloCommand, error) {
	pkt, err := Decode(reqPacket)
	if err != nil {
		return nil, err
	}
	var resp HelloCommand
	if err := DecodeOpQuery(pkt).UnmarshalBSON(&resp); err != nil {
		return nil, err
	}

	if !resp.IsValid() {
		return nil, fmt.Errorf("invalid hello command")
	}
	return &resp, nil
}

func NewHelloCommandPacket(req *HelloCommand, requestID, flagBits, numberToSkip, numberToReturn uint32) (*Packet, error) {
	helloCommandBytes, err := bson.Marshal(req)
	// helloCommandBytes, err := bson.MarshalExtJSON(req, false, false)
	if err != nil {
		return nil, fmt.Errorf("failed encode hello command, err=%v", err)
	}
	fullCollectionName := fmt.Sprintf("%s.$cmd", req.SpeculativeAuthenticate.Database)
	var header PacketHeader
	frameSize :=
		binary.Size(flagBits) +
			len(fullCollectionName) + 1 + // 0x00 delimiter
			binary.Size(numberToSkip) +
			binary.Size(numberToReturn) +
			len(helloCommandBytes)
	frame := make([]byte, frameSize)

	binary.LittleEndian.PutUint32(frame[0:4], flagBits)
	idx := len(fullCollectionName) + 1 // 0x00 delimiter
	_ = copy(frame[4:idx+4], fullCollectionName)

	idx += 4
	binary.LittleEndian.PutUint32(frame[idx:idx+4], numberToSkip)
	idx += 4
	binary.LittleEndian.PutUint32(frame[idx:idx+4], numberToReturn)
	idx += 4
	if copied := copy(frame[idx:], helloCommandBytes); copied != len(helloCommandBytes) {
		return nil, fmt.Errorf("did not copied full frame, copied=%v, data=%v", copied, len(helloCommandBytes))
	}
	return &Packet{
		MessageLength: uint32(binary.Size(header) + frameSize),
		RequestID:     requestID,
		OpCode:        OpQueryType,
		Frame:         frame,
	}, nil
}

type SaslContinueRequest struct {
	SaslContinue   int32  `bson:"saslContinue"`
	ConversationID int32  `bson:"conversationId"`
	Payload        []byte `bson:"payload"`
	Database       string `bson:"$db"`
}

func NewSaslContinuePacket(req *SaslContinueRequest, requestID, flagBits uint32) (*Packet, error) {
	data, err := bson.Marshal(req)
	if err != nil {
		return nil, err
	}
	frameSize :=
		binary.Size(flagBits) +
			len(data) +
			1 // document kind body
	frame := make([]byte, frameSize)
	binary.LittleEndian.PutUint32(frame[0:4], flagBits)

	_ = copy(frame[5:], data)
	return &Packet{
		MessageLength: uint32(frameSize + 16),
		RequestID:     requestID,
		OpCode:        OpMsgType,
		Frame:         frame,
	}, nil
}
