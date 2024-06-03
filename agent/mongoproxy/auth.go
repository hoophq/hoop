package mongoproxy

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/mongotypes"
	"github.com/xdg-go/scram"
	"go.mongodb.org/mongo-driver/bson"
)

const (
	// defaultRequestID is used as request id to all calls to the mongo server.
	// Using a zero value doesn't seem to impact the authentication conversation
	defaultRequestID uint32 = 0
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

	helloCommand, serverAuthMechanism, err := p.decodeClientHelloCommand(authPkt)
	if err != nil {
		return
	}

	clientAuthMechanism := helloCommand.SpeculativeAuthenticate.Mechanism
	clientAuthPayload := helloCommand.CopyAuthPayload()
	client, err := newScramClient(serverAuthMechanism, p.username, p.password)
	if err != nil {
		return
	}

	conversation := client.NewConversation()
	helloCommand.SpeculativeAuthenticate = newSaslRequest(serverAuthMechanism, p.authSource, conversation)
	flagBits := binary.LittleEndian.Uint32(authPkt.Frame[:4])
	newHelloCmd, err := helloCommand.Encode(defaultRequestID, flagBits)
	if err != nil {
		return err
	}
	cinfo := helloCommand.ClientInfo
	log.With("conn", p.connectionID).Infof("decoded client hello: clientmech=%v, servermech=%v, host=%v, app=%v, driver=%v/%v, os=%v/%v, platform=%v",
		clientAuthMechanism, serverAuthMechanism, p.hostAddr, cinfo.ApplicationName(), cinfo.Driver.Name,
		cinfo.Driver.Version, cinfo.OS.Type, cinfo.OS.Architecture, cinfo.Platform)
	respPkt, err := p.writeAndReadNextPacket(p.serverRW, p.serverRW, newHelloCmd)
	if err != nil {
		return err
	}
	authReply, err := mongotypes.DecodeServerAuthReply(respPkt)
	if err != nil {
		return err
	}

	if authReply.SpeculativeAuthenticate == nil {
		return fmt.Errorf("unable to negotiate authentication for %s, please contact the administrator", p.authSource)
	}
	saslResp := mongotypes.SASLResponse{
		ConversationID: authReply.SpeculativeAuthenticate.ConversationID,
		Code:           authReply.SpeculativeAuthenticate.Code,
		Done:           authReply.SpeculativeAuthenticate.Done,
		Payload:        authReply.SpeculativeAuthenticate.Payload,
	}
	log.With("conn", p.connectionID).Infof("decoded hello reply: %v", authReply)

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
	return p.bypassClientAuth(clientAuthMechanism, clientAuthPayload, authReply)
}

// bypassClientAuth generates a scram server with hard-coded credentials to bypass the client authentication.
// The client must use the same credentials provided by the scram server
func (p *proxy) bypassClientAuth(authMechanism string, clientAuthPayload []byte, srvReply *mongotypes.AuthResponseReply) error {
	srv, err := newScramServerWithHardCodedCredentials(authMechanism)
	if err != nil {
		return err
	}
	conversation := srv.NewConversation()
	challengeResp, err := conversation.Step(string(clientAuthPayload))
	if err != nil {
		return fmt.Errorf("client auth: failed validating conversation, err=%v", err)
	}

	srvReply.SaslSupportedMechs = []string{authMechanism}
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
	saslPayload, err := mongotypes.DecodeSASLContinueRequest(saslContinuePkt)
	if err != nil {
		return fmt.Errorf("client auth: %v", err)
	}

	finalResp, err := conversation.Step(string(saslPayload))
	if err != nil {
		return fmt.Errorf("client auth: fail validating final conversation, reason=%v", err)
	}
	pkt := mongotypes.NewScramServerDoneResponse([]byte(finalResp))
	_, err = p.clientW.Write(pkt.Encode())
	return err
}

// decodeClientHelloCommand returns the hello command from the client authentication packet and
// negotiate with the server the auth mechanism by providing the user and database.
func (p *proxy) decodeClientHelloCommand(authPkt *mongotypes.Packet) (*mongotypes.HelloCommand, string, error) {
	requestID := authPkt.RequestID
	flagBits := binary.LittleEndian.Uint32(authPkt.Frame[:4])
	hello, err := mongotypes.DecodeClientHelloCommand(bytes.NewBuffer(authPkt.Encode()))
	if err != nil {
		return nil, "", err
	}
	if hello.SpeculativeAuthenticate == nil {
		return nil, "", errNonSpeculativeAuthConnection
	}
	primaryConn, err := p.newPrimaryConnection()
	if err != nil {
		return nil, "", fmt.Errorf("unable to upgrade connection to primary, %v", err)
	}
	// upgrade connection to authenticate with the primary
	if primaryConn != nil {
		p.serverRW = primaryConn
	}
	discover := &mongotypes.HelloCommand{
		IsMaster:                hello.IsMaster,
		HelloOK:                 hello.HelloOK,
		SaslSupportedMechs:      toStrPtr(fmt.Sprintf("%s.%s", p.authSource, p.username)),
		SpeculativeAuthenticate: &mongotypes.SaslRequest{Database: p.authSource},
		ClientInfo:              hello.ClientInfo,
	}

	helloPkt, err := discover.Encode(requestID, flagBits)
	if err != nil {
		return nil, "", fmt.Errorf("failed encoding hello packet: %v", err)
	}
	respPkt, err := p.writeAndReadNextPacket(p.serverRW, p.serverRW, helloPkt)
	if err != nil {
		return nil, "", fmt.Errorf("failed reading auth response from discover request: %v", err)
	}
	authReply, err := mongotypes.DecodeServerAuthReply(respPkt)
	if err != nil {
		return nil, "", fmt.Errorf("failed decoding server auth reply: %v", err)
	}
	// if the server didn't responded, use the client advertise mechanism
	if len(authReply.SaslSupportedMechs) == 0 {
		log.Warnf("server did not respond with any supported mechanisms, default to client %v",
			hello.SpeculativeAuthenticate.Mechanism)
		return hello, hello.SpeculativeAuthenticate.Mechanism, nil
	}
	for _, serverMech := range authReply.SaslSupportedMechs {
		if serverMech == scramSHA1 || serverMech == scramSHA256 {
			return hello, serverMech, nil
		}
	}
	return nil, "", fmt.Errorf("unable to obtain supported mechanism from the server, supported-mechs=%v",
		authReply.SaslSupportedMechs)
}

// newPrimaryConnection returns a new connection with the primary server
func (p *proxy) newPrimaryConnection() (net.Conn, error) {
	serverConn, err := p.newFreshConnection(p.hostAddr)
	if err != nil {
		return nil, err
	}
	defer serverConn.Close()

	helloPkt, err := (&mongotypes.HelloCommand{
		IsMaster: 1,
		HelloOK:  true,
		// use unknown type, we only care about discovering the primary
		ClientInfo:  mongotypes.ClientInfo{OS: mongotypes.ClientOS{Type: "unknown"}},
		Compression: []any{},
	}).Encode(1, 0)
	if err != nil {
		return nil, fmt.Errorf("failed encoding hello command: %v", err)
	}
	srvReply, err := p.writeAndReadNextPacket(serverConn, serverConn, helloPkt)
	if err != nil {
		return nil, fmt.Errorf("failed obtaining server response: %v", err)
	}

	authReply, err := mongotypes.DecodeServerAuthReply(srvReply)
	if err != nil {
		return nil, fmt.Errorf("failed decoding server auth reply: %v", err)
	}
	if authReply.Primary == "" {
		// dump the hex server response to stdout
		srvReply.Dump()
		return nil, fmt.Errorf("failed decoding hello reply from server")
	}
	if authReply.Primary == p.hostAddr {
		log.With("conn", p.connectionID).Infof("connection is already primary")
		return nil, nil
	}
	log.With("conn", p.connectionID).Infof("obtaining new fresh connection with primary %v", authReply.Primary)
	serverPrimaryConn, err := p.newFreshConnection(authReply.Primary)
	if err != nil {
		return nil, fmt.Errorf("failed obtaining primary connection: %v", err)
	}
	return serverPrimaryConn, nil
}

func (p *proxy) newFreshConnection(hostAddr string) (net.Conn, error) {
	host, _, _ := strings.Cut(hostAddr, ":")
	var tlsConfig *tlsProxyConfig
	if p.tlsProxyConfig != nil {
		tlsConfig = &tlsProxyConfig{
			tlsInsecure:           p.tlsProxyConfig.tlsInsecure,
			serverName:            host,
			tlsCAFile:             p.tlsProxyConfig.tlsCAFile,
			tlsCertificateKeyFile: p.tlsProxyConfig.tlsCertificateKeyFile,
		}
	}
	serverConn, err := net.DialTimeout("tcp4", hostAddr, time.Second*5)
	if err != nil {
		return nil, fmt.Errorf("failed dialing to server %v, err=%s", hostAddr, err)
	}
	tlsConn, err := p.tlsClientHandshake(serverConn, tlsConfig)
	if err != nil {
		_ = serverConn.Close()
		return nil, fmt.Errorf("tls handshake failed: %v", err)
	}
	if tlsConn != nil {
		serverConn = tlsConn
	}
	return serverConn, nil
}

func newSaslRequest(authMechanism, authSource string, conversation *scram.ClientConversation) *mongotypes.SaslRequest {
	// it's safe to ignore the error from the first message
	step, _ := conversation.Step("")
	return &mongotypes.SaslRequest{
		SaslStart: 1,
		Mechanism: authMechanism,
		Payload:   []byte(step),
		Database:  authSource,
		Options:   mongotypes.SaslOptions{SkipEmptyExchange: true},
	}
}

func toStrPtr(v string) *string { return &v }
