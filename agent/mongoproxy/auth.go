package mongoproxy

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/mongotypes"
	"github.com/xdg-go/scram"
	"github.com/xdg-go/stringprep"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type SASLResponse struct {
	ConversationID int32  `bson:"conversationId"`
	Code           int32  `bson:"code,omitempty"`
	Done           bool   `bson:"done"`
	Payload        []byte `bson:"payload"`
}

func (r *proxy) readNextAuthPacket(reader io.Reader) (*mongotypes.Packet, error) {
	pkt, err := mongotypes.Decode(reader)
	if err != nil {
		return nil, err
	}
	return pkt, err
}

func newScramSHA256Client(username, password string) (*scram.Client, error) {
	passprep, err := stringprep.SASLprep.Prepare(password)
	if err != nil {
		return nil, fmt.Errorf("error SASLprepping password: %v", err)
	}
	client, err := scram.SHA256.NewClientUnprepped(username, passprep, "")
	if err != nil {
		return nil, fmt.Errorf("error initializing SCRAM-SHA-256 client: %v", err)
	}
	return client.WithMinIterations(4096), nil
}

func newSaslRequest(conversation *scram.ClientConversation) (*mongotypes.SaslRequest, error) {
	step, err := conversation.Step("")
	if err != nil {
		return nil, err
	}
	payload := []byte(step)
	return &mongotypes.SaslRequest{
		SaslStart: 1,
		Mechanism: "SCRAM-SHA-256",
		Payload:   payload,
		Database:  "admin",
		Options:   mongotypes.SaslOptions{SkipEmptyExchange: true},
	}, nil
}

func (p *proxy) writeOperation(pkt *mongotypes.Packet) (*mongotypes.Packet, error) {
	if _, err := p.serverRW.Write(pkt.Encode()); err != nil {
		return nil, fmt.Errorf("fail write to mongo server, err=%v", err)
	}
	return p.readNextAuthPacket(p.serverRW)
}

// TODO: validate the mechanism, only scram is accepted!
func (p *proxy) handleServerAuth(authPkt *mongotypes.Packet) (bypass bool, err error) {
	if authPkt.OpCode != mongotypes.OpQueryType {
		return true, nil
	}

	saslRequest := map[string]any{}
	authDecQuery := mongotypes.DecodeOpQuery(authPkt)
	err = authDecQuery.UnmarshalBSON(&saslRequest)
	if err != nil {
		return false, err
	}
	_, ok := saslRequest["speculativeAuthenticate"]
	if !ok {
		// it's not a speculative packet, bypass
		return true, nil
	}
	client, err := newScramSHA256Client("root", "1a2b3c4d")
	if err != nil {
		return
	}
	conversation := client.NewConversation()
	saslStartRequest, err := newSaslRequest(conversation)
	if err != nil {
		return
	}
	helloCommand, err := mongotypes.DecodeClientHelloCommand(bytes.NewBuffer(authPkt.Encode()))
	if err != nil {
		return false, err
	}
	helloCommand.SpeculativeAuthenticate = *saslStartRequest
	flagBits := binary.LittleEndian.Uint32(authPkt.Frame[:4])
	newHelloCmd, err := mongotypes.NewHelloCommandPacket(
		helloCommand,
		authPkt.RequestID,
		flagBits,
		binary.LittleEndian.Uint32([]byte{0x00, 0x00, 0x00, 0x00}),
		binary.LittleEndian.Uint32([]byte{0xff, 0xff, 0xff, 0xff}),
	)
	if err != nil {
		return false, err
	}
	log.Infof("writing hello command, connid=%v, validconv=%v", p.connectionID, conversation.Valid())
	newHelloCmd.Dump()
	respPkt, err := p.writeOperation(newHelloCmd)
	if err != nil {
		return false, err
	}

	log.Infof("read sasl response from server, connid=%v", p.connectionID)
	respPkt.Dump()

	respObj := map[string]any{}
	if err := bson.Unmarshal(respPkt.Frame[20:], &respObj); err != nil {
		return false, err
	}
	authSpec, ok := respObj["speculativeAuthenticate"].(map[string]any)
	if !ok {
		return false, fmt.Errorf("failed decoding auth response from server")
	}
	saslPayload := authSpec["payload"].(primitive.Binary)
	code, _ := authSpec["code"].(int32)
	saslResp := SASLResponse{
		ConversationID: authSpec["conversationId"].(int32),
		Code:           code,
		Done:           authSpec["done"].(bool),
		Payload:        saslPayload.Data,
	}
	log.Infof("sasl continue loop attempt, connid=%v", p.connectionID)

	// requestID := authPkt.RequestID
	cid := saslResp.ConversationID
	// var payload string
	i := 0
	for {
		fmt.Printf("idx=%v, SASLRESP: %+v\n", i, saslResp)
		i++
		if saslResp.Code != 0 {
			return false, fmt.Errorf("unable to authenticate, wrong code response (!= 0)")
		}
		payload, err := conversation.Step(string(saslResp.Payload))
		if err != nil {
			return false, fmt.Errorf("failed generation step: %v", err)
		}
		if saslResp.Done && conversation.Done() {
			return false, nil
		}
		log.Infof("writing sasl continue packet, connid=%v, convvalid=%v", p.connectionID, conversation.Valid())
		// requestID++
		pkt, err := mongotypes.NewSaslContinuePacket(&mongotypes.SaslContinueRequest{
			SaslContinue:   1,
			ConversationID: int32(cid),
			Payload:        []byte(payload),
			Database:       "admin",
		}, 0, 0)
		if err != nil {
			return false, fmt.Errorf("failed creating sasl continue packet")
		}
		pkt.Dump()
		respPkt, err = p.writeOperation(pkt)
		if err != nil {
			return false, fmt.Errorf("failed writing operation: %v", err)
		}
		log.Infof("response sasl continue")
		respPkt.Dump()
		if err := bson.Unmarshal(respPkt.Frame[5:], &saslResp); err != nil {
			return false, fmt.Errorf("unmarshal error: %v", err)
		}
	}
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
	SpeculativeAuthenticate      SASLResponse       `bson:"speculativeAuthenticate"`
	OK                           float64            `bson:"ok"`
}

func (r *AuthResponseReply) Size() int {
	enc, err := bson.Marshal(r)
	if err != nil {
		return -1
	}
	return len(enc)
}

func newServerAuthReply(authPayload []byte, connectionID int) *mongotypes.Packet {
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
		SpeculativeAuthenticate: SASLResponse{
			ConversationID: 1,
			Done:           false,
			Payload:        authPayload,
		},
		OK: 1,
	}
	pkt := mongotypes.Packet{}
	pkt.Frame = make([]byte, reply.Size()+20)
	out, _ := bson.Marshal(&reply)
	copy(pkt.Frame[36:], out)
	// pkt := mongotypes.Packet{}
	return &pkt
	// make(pkt.Frame,
}

// handleClientAuth bypass the client authentication
func (p *proxy) handleClientAuth(initPkt *mongotypes.Packet) error {
	// mongotypes.Packet{}
	return nil
}
