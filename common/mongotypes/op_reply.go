package mongotypes

import (
	"encoding/binary"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
)

type SASLResponse struct {
	ConversationID int32  `bson:"conversationId"`
	Code           int32  `bson:"code,omitempty"`
	Done           bool   `bson:"done"`
	Payload        []byte `bson:"payload"`
}

type TopologyVersion struct {
	ProcessID primitive.ObjectID `bson:"processId"`
	Counter   int64              `bson:"counter"`
}

type AuthResponseReply struct {
	HelloOk                      bool               `bson:"helloOk"`
	IsMaster                     bool               `bson:"ismaster"`
	TopologyVersion              TopologyVersion    `bson:"topologyVersion"`
	MaxBsonObjectSize            int32              `bson:"maxBsonObjectSize"`
	MaxMessageSizeBytes          int32              `bson:"maxMessageSizeBytes"`
	MaxWriteBatchSize            int32              `bson:"maxWriteBatchSize"`
	LocalTime                    primitive.DateTime `bson:"localTime"`
	LogicalSessionTimeoutMinutes int32              `bson:"logicalSessionTimeoutMinutes"`
	ConnectionID                 int32              `bson:"connectionId"`
	MinWireVersion               int32              `bson:"minWireVersion"`
	MaxWireVersion               int32              `bson:"maxWireVersion"`
	ReadOnly                     bool               `bson:"readOnly"`
	SaslSupportedMechs           []string           `bson:"saslSupportedMechs,omitempty"`
	SpeculativeAuthenticate      SASLResponse       `bson:"speculativeAuthenticate"`
	OK                           float64            `bson:"ok"`
}

func NewServerAuthReply(authPayload []byte, requestID, responseTo, connectionID int) (*Packet, error) {
	reply := AuthResponseReply{
		HelloOk:  true,
		IsMaster: true,
		TopologyVersion: TopologyVersion{
			ProcessID: primitive.NewObjectID(),
			Counter:   0,
		},
		MaxBsonObjectSize:            16777216,
		MaxMessageSizeBytes:          48000000,
		MaxWriteBatchSize:            100000,
		LocalTime:                    primitive.NewDateTimeFromTime(time.Now().UTC()),
		LogicalSessionTimeoutMinutes: 30,
		ConnectionID:                 int32(connectionID),
		MinWireVersion:               0,
		MaxWireVersion:               21,
		ReadOnly:                     false,
		SaslSupportedMechs:           []string{"SCRAM-SHA-256"},
		SpeculativeAuthenticate: SASLResponse{
			ConversationID: 1,
			Done:           false,
			Payload:        authPayload,
		},
		OK: 1,
	}
	data, err := bson.Marshal(&reply)
	if err != nil {
		return nil, err
	}
	frameSize := 4 + // reply flags
		8 + // cursor id
		4 + // starting from
		4 + // number returned
		len(data)
	frame := make([]byte, frameSize)
	binary.LittleEndian.PutUint32(frame[16:20], 1) // hard-coded number returned
	_ = copy(frame[20:], data)
	return &Packet{
		MessageLength: uint32(frameSize + 16),
		RequestID:     uint32(requestID),
		ResponseTo:    uint32(responseTo),
		OpCode:        OpReplyType,
		Frame:         frame,
	}, nil
}

type SaslDoneRequest struct {
	ConversationID int32  `bson:"conversationId"`
	Done           bool   `bson:"done"`
	Ok             bool   `bson:"ok"`
	Payload        []byte `bson:"payload"`
}

func NewScramServerDoneResponse(requestID, responseTo, convID int, payload []byte) *Packet {
	saslDoneDoc := bsoncore.BuildDocumentFromElements(nil,
		bsoncore.AppendInt32Element(nil, "conversationId", int32(convID)),
		bsoncore.AppendBinaryElement(nil, "payload", 0x00, payload),
		bsoncore.AppendBooleanElement(nil, "done", true),
		bsoncore.AppendInt32Element(nil, "ok", 1),
	)
	var flagBits uint32
	frameSize :=
		binary.Size(flagBits) +
			len(saslDoneDoc) +
			1 // document kind body
	frame := make([]byte, frameSize)
	_ = copy(frame[5:], saslDoneDoc)
	return &Packet{
		MessageLength: uint32(frameSize + 16),
		RequestID:     uint32(requestID),
		ResponseTo:    uint32(responseTo),
		OpCode:        OpMsgType,
		Frame:         frame,
	}
}

func NewSaslDonePacket(requestID, flagBits uint32) (*Packet, error) {
	req := &SaslDoneRequest{ConversationID: 1, Done: true, Ok: true}
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
