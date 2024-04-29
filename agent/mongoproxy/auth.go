package mongoproxy

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/mongotypes"
	"github.com/xdg-go/scram"
	"github.com/xdg-go/stringprep"
	"go.mongodb.org/mongo-driver/bson"
)

const (
	defaultAuthMechanism = "SCRAM-SHA-256"
	defaultProxyUser     = "noop"
	defaultProxyPwd      = "noop"
	// defaultRequestID is used as request id to all calls to the mongo server.
	// Using a zero value doesn't seem to impact the authentication conversation
	defaultRequestID uint32 = 0
	// changes minimum required scram PBKDF2 iteration count.
	defaultScramMinIterations int = 4096
)

var errNonSpeculativeAuthConnection = errors.New("non speculative authentication connection")

func (p *proxy) writeAndReadNextPacket(w io.Writer, reader io.Reader, pkt *mongotypes.Packet) (*mongotypes.Packet, error) {
	if _, err := w.Write(pkt.Encode()); err != nil {
		return nil, fmt.Errorf("fail write to mongo server, err=%v", err)
	}
	pkt, err := mongotypes.Decode(reader)
	if err != nil {
		return nil, err
	}
	return pkt, err
}

// handleServerAuth authenticate the connection with speculative authentication.
// When this attribute is not set the connection is considered as a monitoring socket
// and it should not be handled by this function.
func (p *proxy) handleServerAuth(authPkt *mongotypes.Packet) (err error) {
	if authPkt.OpCode != mongotypes.OpQueryType {
		return errNonSpeculativeAuthConnection
	}

	helloCommand, err := mongotypes.DecodeClientHelloCommand(bytes.NewBuffer(authPkt.Encode()))
	if err != nil {
		return
	}
	if helloCommand.SpeculativeAuthenticate == nil {
		return errNonSpeculativeAuthConnection
	}
	if helloCommand.SpeculativeAuthenticate.Mechanism != defaultAuthMechanism {
		return fmt.Errorf("mechanism %v is not supported", helloCommand.SpeculativeAuthenticate.Mechanism)
	}

	client, err := newScramSHA256Client(p.username, p.password)
	if err != nil {
		return
	}
	conversation := client.NewConversation()
	clientAuthPayload := helloCommand.SpeculativeAuthenticate.Payload
	helloCommand.SpeculativeAuthenticate = newSaslRequest(p.authSource, conversation)
	flagBits := binary.LittleEndian.Uint32(authPkt.Frame[:4])
	newHelloCmd, err := helloCommand.Encode(defaultRequestID, flagBits)
	if err != nil {
		return err
	}
	cinfo := helloCommand.ClientInfo
	log.With("conn", p.connectionID).Infof("decoded hello authentication packet from client, host=%v, app=%v, driver=%v/%v, os=%v/%v, platform=%v",
		p.host, cinfo.ApplicationName(), cinfo.Driver.Name, cinfo.Driver.Version, cinfo.OS.Type, cinfo.OS.Architecture, cinfo.Platform)
	respPkt, err := p.writeAndReadNextPacket(p.serverRW, p.serverRW, newHelloCmd)
	if err != nil {
		return err
	}
	authReply, err := mongotypes.DecodeServerAuthReply(respPkt)
	if err != nil {
		return err
	}
	if authReply.SpeculativeAuthenticate == nil {
		return fmt.Errorf("missing speculative authentication section")
	}
	saslResp := mongotypes.SASLResponse{
		ConversationID: authReply.SpeculativeAuthenticate.ConversationID,
		Code:           authReply.SpeculativeAuthenticate.Code,
		Done:           authReply.SpeculativeAuthenticate.Done,
		Payload:        authReply.SpeculativeAuthenticate.Payload,
	}
	log.With("conn", p.connectionID).Infof("decoded hello reply from server: %v", authReply)

	for {
		if saslResp.Code != 0 {
			return fmt.Errorf("unable to authenticate, wrong code response: %v", saslResp.Code)
		}
		payload, err := conversation.Step(string(saslResp.Payload))
		if err != nil {
			return fmt.Errorf("fail validating conversation: %v", err)
		}
		if saslResp.Done && conversation.Done() {
			break
		}
		log.With("conn", p.connectionID).Infof("writing SASL continue packet")
		saslContinuePkt := mongotypes.NewSaslContinuePacket(defaultRequestID, saslResp.ConversationID, []byte(payload), p.authSource)
		authPkt, err = p.writeAndReadNextPacket(p.serverRW, p.serverRW, saslContinuePkt)
		if err != nil {
			return fmt.Errorf("failed write SASL continue packet: %v", err)
		}
		if err := bson.Unmarshal(authPkt.Frame[5:], &saslResp); err != nil {
			authPkt.Dump()
			return fmt.Errorf("failed to decode SASL continue packet: %v", err)
		}
	}
	log.With("conn", p.connectionID).Infof("connection authenticated with server")
	return p.bypassClientAuth(clientAuthPayload, authReply)
}

// bypassClientAuth generates a scram server with hard-coded credentials to bypass the client authentication.
// The client must use the same credentials provided by the scram server
func (p *proxy) bypassClientAuth(clientAuthPayload []byte, srvReply *mongotypes.AuthResponseReply) error {
	srv, err := newScramServerWithHardCodedCredentials()
	if err != nil {
		return err
	}
	conversation := srv.NewConversation()
	challengeResp, err := conversation.Step(string(clientAuthPayload))
	if err != nil {
		return fmt.Errorf("client auth: failed validating conversation, err=%v", err)
	}
	srvReply.SpeculativeAuthenticate = &mongotypes.SASLResponse{
		ConversationID: 1,
		Done:           false,
		Payload:        []byte(challengeResp),
	}
	srvReplyPkt, err := srvReply.Encode(defaultRequestID)
	if err != nil {
		return fmt.Errorf("client auth: failed encoding server reply packet, reason=%v", err)
	}

	saslContinuePkt, err := p.writeAndReadNextPacket(p.clientW, p.clientInitBuffer, srvReplyPkt)
	if err != nil {
		return err
	}
	var saslContinue mongotypes.SaslContinueRequest
	err = bson.Unmarshal(saslContinuePkt.Frame[5:], &saslContinue)
	if err != nil {
		return fmt.Errorf("client auth: failed unmarshalling SASL continue packet, reason=%v", err)
	}

	if saslContinue.Payload == nil {
		return fmt.Errorf("client auth: failed decoding (empty) SAS continue payload")
	}

	finalResp, err := conversation.Step(string(saslContinue.Payload))
	if err != nil {
		return fmt.Errorf("client auth: fail validating final conversation, reason=%v", err)
	}
	pkt := mongotypes.NewScramServerDoneResponse([]byte(finalResp))
	_, err = p.clientW.Write(pkt.Encode())
	return err
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
	return client.WithMinIterations(defaultScramMinIterations), nil
}

func newSaslRequest(authSource string, conversation *scram.ClientConversation) *mongotypes.SaslRequest {
	// it's safe to ignore the error from the first message
	step, _ := conversation.Step("")
	return &mongotypes.SaslRequest{
		SaslStart: 1,
		Mechanism: defaultAuthMechanism,
		Payload:   []byte(step),
		Database:  authSource,
		Options:   mongotypes.SaslOptions{SkipEmptyExchange: true},
	}
}

func newScramServerWithHardCodedCredentials() (*scram.Server, error) {
	client, err := newScramSHA256Client(defaultProxyUser, defaultProxyPwd)
	if err != nil {
		return nil, err
	}
	stored := client.GetStoredCredentials(scram.KeyFactors{Salt: "server-nonce", Iters: defaultScramMinIterations})
	return scram.SHA256.NewServer(func(s string) (scram.StoredCredentials, error) {
		return stored, nil
	})
}
