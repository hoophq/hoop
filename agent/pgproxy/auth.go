package pgproxy

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/lib/pq/scram"
	"github.com/runopsio/hoop/common/log"
	pgtypes "github.com/runopsio/hoop/common/pgtypes"
)

func (r *proxy) readNextAuthPacket(reader io.Reader, pktType pgtypes.PacketType) (*pgtypes.Packet, error) {
	_, pkt, err := pgtypes.DecodeTypedPacket(reader)
	if err != nil {
		return nil, err
	}
	switch pkt.Type() {
	case pktType:
		return pkt, nil
	case pgtypes.ServerErrorResponse:
		r.clientW.Write(pkt.Encode())
		return nil, fmt.Errorf("received error response from server trying to authenticate")
	default:
		return nil, fmt.Errorf("unknown packet, expected=[%X], found=[%X]", pktType, pkt.Type())
	}
}

func (p *proxy) handleAuth(startupMessage *pgtypes.Packet) error {
	if _, err := p.serverRW.Write(startupMessage.Encode()); err != nil {
		return fmt.Errorf("failed writing startup message to server, reason=%v", err)
	}
	pkt, err := p.readNextAuthPacket(p.serverRW, pgtypes.ServerAuth)
	if err != nil {
		return err
	}
	authType := toAuthType(pkt)
	switch authType {
	case pgtypes.ServerAuthenticationSASL:
		log.Infof("server supports SASL authentication")
		return p.processSaslAuth()
	case pgtypes.ServerAuthenticationMD5Password:
		log.Infof("server supports MD5 authentication")
		return p.processMd5Auth(pkt)
	default:
		return fmt.Errorf("authentication type [%v] not supported", authType)
	}
}

func (p *proxy) processMd5Auth(pkt *pgtypes.Packet) error {
	rawPkt := pkt.Encode()
	saltVal := rawPkt[len(rawPkt)-4:]
	authDataString := fmt.Sprintf("md5%s", md5s(md5s(p.password+p.username)+string(saltVal)))
	authData := append([]byte(authDataString), byte(0))
	resp := pgtypes.NewPasswordMessage(authData)
	if _, err := p.serverRW.Write(resp.Encode()); err != nil {
		return fmt.Errorf("failed writing MD5 password to server, reason=%v", err)
	}
	pkt, err := p.readNextAuthPacket(p.serverRW, pgtypes.ServerAuth)
	if err != nil {
		return err
	}
	if toAuthType(pkt) != pgtypes.ServerAuthenticationOK {
		log.Infof("receive a non-ok auth response from server")
		pkt.Dump()
		return fmt.Errorf("md5 authentication failed")
	}
	log.Infof("authenticated (md5) user %v with success", p.username)
	if _, err := p.clientW.Write(pgtypes.NewAuthenticationOK().Encode()); err != nil {
		return fmt.Errorf("failed writing auth ok to client, reason=%v", err)
	}
	return nil
}

func (p *proxy) processSaslAuth() error {
	sc := scram.NewClient(sha256.New, p.username, p.password)
	_ = sc.Step(nil)
	if sc.Err() != nil {
		return fmt.Errorf("SCRAM-SHA-256 error: %v", sc.Err())
	}
	resp := pgtypes.NewSASLInitialResponsePacket(sc.Out())
	if _, err := p.serverRW.Write(resp.Encode()); err != nil {
		return fmt.Errorf("failed writing SASL initial response to server, reason=%v", err)
	}
	pkt, err := p.readNextAuthPacket(p.serverRW, pgtypes.ServerAuth)
	if err != nil {
		return err
	}
	authType := toAuthType(pkt)
	if authType != pgtypes.ServerAuthenticationSASLContinue {
		return fmt.Errorf("unexpected auth type, expected=[%v], found=[%v]",
			pgtypes.ServerAuthenticationSASLContinue, authType)
	}
	log.Infof("processing auth SASL continue phase")
	rawPkt := pkt.Encode()
	// rawPkt [TYPE|HEADER|AUTH-SIZE|AUTHPAYLOAD]
	authPayload := make([]byte, len(rawPkt[9:]))
	copy(authPayload, rawPkt[9:])
	_ = sc.Step(authPayload)
	if err := sc.Err(); err != nil {
		return fmt.Errorf("SCRAM-SHA-256 error: %v", err)
	}
	resp = pgtypes.NewSASLResponse(sc.Out())
	if _, err := p.serverRW.Write(resp.Encode()); err != nil {
		return fmt.Errorf("failed writing SASL response to server, reason=%v", err)
	}
	pkt, err = p.readNextAuthPacket(p.serverRW, pgtypes.ServerAuth)
	if err != nil {
		return err
	}
	authType = toAuthType(pkt)
	if authType != pgtypes.ServerAuthenticationSASLFinal {
		return fmt.Errorf("unexpected auth type, expected=[%v], found=[%v]",
			pgtypes.ServerAuthenticationSASLFinal, authType)
	}
	log.Infof("processing auth SASL final phase")
	rawPkt = pkt.Encode()
	_ = sc.Step(rawPkt[9:])
	if err := sc.Err(); err != nil {
		return fmt.Errorf("SCRAM-SHA-256 error: %v", err)
	}

	pkt, err = p.readNextAuthPacket(p.serverRW, pgtypes.ServerAuth)
	if err != nil {
		return err
	}
	authType = toAuthType(pkt)
	if authType != pgtypes.ServerAuthenticationOK {
		log.Infof("receive a non-ok auth response from server")
		pkt.Dump()
		return fmt.Errorf("sasl authentication failed")
	}

	log.Infof("authenticated (sasl) user %v with success", p.username)
	if _, err := p.clientW.Write(pgtypes.NewAuthenticationOK().Encode()); err != nil {
		return fmt.Errorf("failed writing auth ok to client, reason=%v", err)
	}
	return nil
}

func toAuthType(pkt *pgtypes.Packet) pgtypes.AuthPacketType {
	rawPkt := pkt.Encode()
	return pgtypes.AuthPacketType(binary.BigEndian.Uint32(rawPkt[5:9]))
}

func md5s(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum(nil))
}
