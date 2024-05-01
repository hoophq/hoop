package mongotypes

import (
	"encoding/binary"
	"fmt"
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

type lastWriteDate struct {
	LastWriteDate time.Time `bson:"lastWriteDate"`
}

type AuthResponseReply struct {
	Arbiters                     []string           `bson:"arbiters,omitempty"`
	ArbiterOnly                  bool               `bson:"arbiterOnly,omitempty"`
	ClusterTime                  bson.Raw           `bson:"$clusterTime,omitempty"`
	ConnectionID                 int32              `bson:"connectionId"`
	Compression                  []string           `bson:"compression,omitempty"`
	ElectionID                   primitive.ObjectID `bson:"electionId,omitempty"`
	Hidden                       bool               `bson:"hidden,omitempty"`
	Hosts                        []string           `bson:"hosts,omitempty"`
	HelloOK                      bool               `bson:"helloOk,omitempty"`
	IsWritablePrimary            bool               `bson:"isWritablePrimary,omitempty"`
	IsReplicaSet                 bool               `bson:"isreplicaset,omitempty"`
	LastWrite                    *lastWriteDate     `bson:"lastWrite,omitempty"`
	LogicalSessionTimeoutMinutes uint32             `bson:"logicalSessionTimeoutMinutes,omitempty"`
	MaxBSONObjectSize            uint32             `bson:"maxBsonObjectSize"`
	MaxMessageSizeBytes          uint32             `bson:"maxMessageSizeBytes"`
	MaxWriteBatchSize            uint32             `bson:"maxWriteBatchSize"`
	Me                           string             `bson:"me,omitempty"`
	MaxWireVersion               int32              `bson:"maxWireVersion"`
	MinWireVersion               int32              `bson:"minWireVersion"`
	Msg                          string             `bson:"msg,omitempty"`
	OK                           int32              `bson:"ok"`
	Passives                     []string           `bson:"passives,omitempty"`
	Primary                      string             `bson:"primary,omitempty"`
	ReadOnly                     bool               `bson:"readOnly,omitempty"`
	SaslSupportedMechs           []string           `bson:"saslSupportedMechs,omitempty"`
	Secondary                    bool               `bson:"secondary,omitempty"`
	SetName                      string             `bson:"setName,omitempty"`
	SetVersion                   uint32             `bson:"setVersion,omitempty"`
	SpeculativeAuthenticate      *SASLResponse      `bson:"speculativeAuthenticate"`
	Tags                         map[string]string  `bson:"tags,omitempty"`
	TopologyVersion              *TopologyVersion   `bson:"topologyVersion,omitempty"`
}

func (r *AuthResponseReply) String() string {
	return fmt.Sprintf("[%v %v %v] [%v %v] primary=%v, issecondary=%v, ro=%v",
		r.MaxBSONObjectSize, r.MaxMessageSizeBytes, r.MaxWriteBatchSize,
		r.MinWireVersion, r.MaxWireVersion, r.Primary, r.Secondary, r.ReadOnly,
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
		// TODO: it's removed on mongodb 5.1, however it worked
		// in local tests and with the latest atlas mongodb instance.
		// Need to verify if it's going to be a problem
		OpCode: OpReplyType,
		Frame:  frame,
	}, nil
}

func DecodeServerAuthReply(pkt *Packet) (*AuthResponseReply, error) {
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
