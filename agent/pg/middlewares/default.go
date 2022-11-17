package middlewares

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log"
	"strings"

	"github.com/lib/pq/scram"
	"github.com/runopsio/hoop/agent/pg"
	pgtypes "github.com/runopsio/hoop/common/proxy"
)

type middleware struct {
	pgUsername string
	pgPassword string
	pgClient   pg.ResponseWriter
	pgServer   pg.ResponseWriter
	scramc     *scram.Client
}

func New(pgClient, pgServer pg.ResponseWriter, user, passwd string) *middleware {
	return &middleware{
		pgClient:   pgClient,
		pgServer:   pgServer,
		pgUsername: user,
		pgPassword: passwd}
}

// DenyChangePassword prevents users changing their own password
// Direction: client->server
func (m *middleware) DenyChangePassword(next pg.NextFn, pkt *pg.Packet, w pg.ResponseWriter) {
	encPkt := pkt.Encode()
	switch pkt.Type() {
	case pgtypes.ClientParse:
		// dbeaver sends alter user statement in Parse message
		if bytes.Contains(encPkt, []byte(`ALTER USER`)) {
			m.writeClientFeatureNotSupportedErr()
			return
		}
	case pgtypes.ClientSimpleQuery:
		if len(encPkt) >= 15 {
			q := strings.ToLower(string(encPkt[5:15]))
			if q == "alter role" || q == "alter user" {
				m.writeClientFeatureNotSupportedErr()
				return
			}
		}
	}
	next()
}

// ProxyCustomAuth negotiates the authentication providing a custom authentication mechanism
// it supports SASL and md5 authentication. Direction: server->client
func (m *middleware) ProxyCustomAuth(next pg.NextFn, pkt *pg.Packet, w pg.ResponseWriter) {
	if pkt.Type() != pgtypes.ServerAuth {
		next()
		return
	}
	// fmt.Println("executing proxy custom auth -->>")
	rawPkt := pkt.Encode()
	authType := binary.BigEndian.Uint32(rawPkt[5:9])
	switch pgtypes.AuthPacketType(authType) {
	// https://github.com/lib/pq/blob/v1.10.7/conn.go#L1277
	case pgtypes.ServerAuthenticationSASL:
		log.Printf("auth-sasl")
		sc := scram.NewClient(sha256.New, m.pgUsername, m.pgPassword)
		_ = sc.Step(nil)
		if sc.Err() != nil {
			m.writeInvalidPasswordErr(w, "SCRAM-SHA-256 error: %v", sc.Err())
			return
		}
		m.scramc = sc
		resp := pg.NewSASLInitialResponsePacket(sc.Out())
		m.write(m.pgServer, resp.Encode())
	case pgtypes.ServerAuthenticationSASLContinue:
		log.Printf("auth-sasl-continue")
		if m.scramc == nil {
			m.writeInvalidPasswordErr(w, "auth-sasl-continue: scram client is empty")
			return
		}
		// rawPkt [TYPE|HEADER|AUTH-SIZE|AUTHPAYLOAD]
		authPayload := make([]byte, len(rawPkt[9:]))
		copy(authPayload, rawPkt[9:])
		_ = m.scramc.Step(authPayload)
		if err := m.scramc.Err(); err != nil {
			m.writeInvalidPasswordErr(w, "SCRAM-SHA-256 error: %v", err)
			return
		}
		resp := pg.NewSASLResponse(m.scramc.Out())
		m.write(m.pgServer, resp.Encode())
	case pgtypes.ServerAuthenticationSASLFinal:
		log.Printf("auth-sasl-final")
		if m.scramc == nil {
			m.writeInvalidPasswordErr(w, "auth-sasl-final: scram client is empty")
			return
		}
		_ = m.scramc.Step(rawPkt[9:])
		if err := m.scramc.Err(); err != nil {
			m.writeInvalidPasswordErr(w, "SCRAM-SHA-256 error: %v", err)
		}
	// https://github.com/lib/pq/blob/v1.10.7/conn.go#L1209
	case pgtypes.ServerAuthenticationMD5Password:
		log.Printf("auth md5 password")
		rawPkt := pkt.Encode()
		saltVal := rawPkt[len(rawPkt)-4:]
		authDataString := fmt.Sprintf("md5%s", md5s(md5s(m.pgPassword+m.pgUsername)+string(saltVal)))
		authData := append([]byte(authDataString), byte(0))
		resp := pg.NewPasswordMessage(authData)
		m.write(m.pgServer, resp.Encode())
	case pgtypes.ServerAuthenticationOK:
		log.Printf("authentication ok")
		m.write(w, pg.NewAuthenticationOK().Encode())
		return
	default:
		log.Printf("authentication method (%v) not supported", authType)
		m.writeInvalidAuthSpecErr(w, "authentication method (%v) not supported", authType)
	}
}

func (m *middleware) writeInvalidPasswordErr(w pg.ResponseWriter, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	for _, pkt := range pg.NewErrorPacketResponse(msg, pgtypes.LevelError, pgtypes.InvalidPassword) {
		m.write(w, pkt.Encode())
	}
}

func (h *middleware) writeInvalidAuthSpecErr(w pg.ResponseWriter, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	for _, pkt := range pg.NewErrorPacketResponse(msg, pgtypes.LevelError, pgtypes.InvalidAuthorizationSpecification) {
		h.write(w, pkt.Encode())
	}
}

func (h *middleware) write(w pg.ResponseWriter, encPkt []byte) {
	if _, err := w.Write(encPkt); err != nil {
		log.Printf("middleware - failed writing err=%v", err)
	}
}

func (m *middleware) writeClientFeatureNotSupportedErr() {
	packets := pg.NewErrorPacketResponse(
		"unsupported operation",
		pgtypes.LevelNotice,
		pgtypes.FeatureNotSupported)
	for _, pkt := range packets {
		if _, err := m.pgClient.Write(pkt.Encode()); err != nil {
			log.Printf("middleware - failed writing error response to client, err=%v", err)
		}
	}
}

func md5s(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum(nil))
}
