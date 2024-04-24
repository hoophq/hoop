package mongoproxy

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"

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

func (p *proxy) writeOperation(rw io.ReadWriter, pkt *mongotypes.Packet) (*mongotypes.Packet, error) {
	if _, err := rw.Write(pkt.Encode()); err != nil {
		return nil, fmt.Errorf("fail write to mongo server, err=%v", err)
	}
	return p.readNextAuthPacket(rw)
}

func (p *proxy) writeClientOperation(pkt *mongotypes.Packet) (*mongotypes.Packet, error) {
	if _, err := p.clientW.Write(pkt.Encode()); err != nil {
		return nil, fmt.Errorf("fail write to mongo client, err=%v", err)
	}
	return p.readNextAuthPacket(p.clientInitBuffer)
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
	client, err := newScramSHA256Client(p.username, p.password)
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
	var clientAuthPayload []byte
	if helloCommand.SpeculativeAuthenticate != nil {
		clientAuthPayload = helloCommand.SpeculativeAuthenticate.Payload
	}
	log.Infof("client payload before")
	fmt.Println(hex.Dump(clientAuthPayload))
	helloCommand.SpeculativeAuthenticate = saslStartRequest
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
	log.Infof("hello auth packet, connid=%v", p.connectionID)
	authPkt.Dump()
	log.Infof("writing hello command, connid=%v, validconv=%v", p.connectionID, conversation.Valid())
	newHelloCmd.Dump()
	respPkt, err := p.writeOperation(p.serverRW, newHelloCmd)
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
	var err2 error
	// var payload string
	i := 0
	for {
		fmt.Printf("idx=%v, SASLRESP: %+v\n", i, saslResp)
		i++
		if saslResp.Code != 0 {
			err2 = fmt.Errorf("unable to authenticate, wrong code response (!= 0)")
			break
		}
		payload, err := conversation.Step(string(saslResp.Payload))
		if err != nil {
			err2 = fmt.Errorf("failed generation step: %v", err)
			break
		}
		if saslResp.Done && conversation.Done() {
			err2 = nil
			break
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
			err2 = fmt.Errorf("failed creating sasl continue packet, reason=%v", err)
			break
		}
		pkt.Dump()
		respPkt2, err := p.writeOperation(p.serverRW, pkt)
		if err != nil {
			err2 = fmt.Errorf("failed writing operation: %v", err)
			break
		}
		log.Infof("response sasl continue")
		respPkt2.Dump()
		if err := bson.Unmarshal(respPkt2.Frame[5:], &saslResp); err != nil {
			err2 = fmt.Errorf("unmarshal error: %v", err)
			break
		}
	}

	if err2 != nil {
		return false, err2
	}
	return false, p.handleClientAuth(clientAuthPayload, client, int(authPkt.ResponseTo), 2)
	// return false, err2
}

func newFakeServer(user, pwd string) (*scram.Server, error) {
	client, err := newScramSHA256Client(user, pwd)
	if err != nil {
		return nil, err
	}
	stored := client.GetStoredCredentials(scram.KeyFactors{Salt: "serverNonce", Iters: 4096})
	return scram.SHA256.NewServer(func(s string) (scram.StoredCredentials, error) {
		enc := base64.StdEncoding.EncodeToString
		log.Infof("RETURN STORED, server-key=%v, stored-key=%v", enc(stored.ServerKey), enc(stored.StoredKey))
		return stored, nil
	})
}

// handleClientAuth bypass the client authentication
func (p *proxy) handleClientAuth(clientAuthPayload []byte, client *scram.Client, responseTo, connectionID int) error {
	log.Infof("client -> write auth reply from server ...")

	srv, err := newFakeServer("noop", "noop")
	if err != nil {
		return err
	}
	conversation := srv.NewConversation()
	challengeResp, err := conversation.Step(string(clientAuthPayload))
	if err != nil {
		return fmt.Errorf("failed generating fake server step, err=%v", err)
	}
	fakeAuthRespReply, err := mongotypes.NewServerAuthReply([]byte(challengeResp), 4, responseTo, connectionID)
	if err != nil {
		return err
	}
	fakeAuthRespReply.Dump()
	saslContinuePkt, err := p.writeClientOperation(fakeAuthRespReply)
	if err != nil {
		return err
	}
	var saslContinue mongotypes.SaslContinueRequest
	err = bson.Unmarshal(saslContinuePkt.Frame[5:], &saslContinue)
	if err != nil {
		return fmt.Errorf("failed unmarshalling sasl continue, reason=%v", err)
	}

	if saslContinue.Payload == nil {
		return fmt.Errorf("failed decoding (empty) sasl continue payload")
	}

	finalResp, err := conversation.Step(string(saslContinue.Payload))
	if err != nil {
		return fmt.Errorf("fail server final step, reason=%v", err)
	}
	// fmt.Println(hex.Dump(continuePayload))

	// return fmt.Errorf("not implemented, move on")

	log.Info("client -> write final sasl response")
	// saslContinuePkt.Dump()
	pkt := mongotypes.NewScramServerDoneResponse(5, 0, 1, []byte(finalResp))
	_, err = p.clientW.Write(pkt.Encode())
	return err
}
