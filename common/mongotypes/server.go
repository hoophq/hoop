package mongotypes

import (
	"encoding/binary"
	"fmt"

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
	SpeculativeAuthenticate      *SASLResponse      `bson:"speculativeAuthenticate"`
	OK                           float64            `bson:"ok"`
}

func (r *AuthResponseReply) String() string {
	return fmt.Sprintf("maxBsonObjSize=%v, maxMsgSizeBytes=%v, maxWBtchSize=%v, minWireVer=%v, maxWireVer=%v, ro=%v",
		r.MaxBsonObjectSize, r.MaxMessageSizeBytes, r.MaxWriteBatchSize,
		r.MinWireVersion, r.MaxWireVersion, r.ReadOnly,
	)
}

func (r *AuthResponseReply) Encode(requestID uint32) (*Packet, error) {
	data, err := bson.Marshal(r)
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
		ResponseTo:    0,
		OpCode:        OpReplyType,
		Frame:         frame,
	}, nil
}

func DecodeServerAuthReply(pkt *Packet) (*AuthResponseReply, error) {
	if pkt.OpCode != OpReplyType {
		return nil, fmt.Errorf("wrong op code, expected OpReplyType (1)")
	}
	var reply AuthResponseReply
	if err := bson.Unmarshal(pkt.Frame[20:], &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

func NewScramServerDoneResponse(payload []byte) *Packet {
	saslDoneDoc := bsoncore.BuildDocumentFromElements(nil,
		bsoncore.AppendInt32Element(nil, "conversationId", 0),
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
		RequestID:     0,
		ResponseTo:    0,
		OpCode:        OpMsgType,
		Frame:         frame,
	}
}
