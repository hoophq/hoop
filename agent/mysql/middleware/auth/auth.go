// gives authentication proxy capabilities. Most of the parsing logic
// is derived from the mysql driver
//
// https://github.com/go-sql-driver/mysql
package auth

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"

	"github.com/runopsio/hoop/agent/mysql"
	"github.com/runopsio/hoop/agent/mysql/types"
	"github.com/runopsio/hoop/common/log"
)

const (
	minProtocolVersion = 10

	// clientSequence will always be a constant for the supported authentication flows.
	// In case it changes, the client sequence must be computed dynamically
	clientSequence = 2

	phaseInitialHandshake         = "InitialHandkshake"
	phaseClientHandshakeResponse  = "ClientHandshakeResponse"
	phaseHandshakeResponseResult  = "HandshakeResponseResult"
	phaseAuthSwitchResponseResult = "AuthSwitchResponseResult"
	phaseFullAuthPubKeyResponse   = "FullAuthPubKeyResponse"
	phaseReadAuthResult           = "ReadAuthResult"
)

func New(user, passwd string) *authMiddleware {
	return &authMiddleware{
		phase:        phaseInitialHandshake,
		authUser:     user,
		authPassword: passwd,
	}
}

type authMiddleware struct {
	phase             string
	handshakeAuthData []byte
	handshakePlugin   string
	isAuthenticated   bool

	authUser     string
	authPassword string
}

func (m *authMiddleware) Handler(next mysql.NextFn, pkt *types.Packet, cli, srv io.WriteCloser) {
	// When the proxy terminates authenticating with the server in behalf of client
	// all packets flow without any modification.
	if m.isAuthenticated {
		next()
		return
	}

	log.Debugf("source=%s, phase=%v, seq=%v, length=%v", pkt.Source, m.phase, pkt.Seq(), len(pkt.Frame))
	pkt.Dump()
	// the first packet that arrives must be server handshake
	if m.phase == phaseInitialHandshake {
		log.Infof("source=%s, phase=%v - received initial handshake from server", pkt.Source, m.phase)
		hshk, err := parseHandshakePacket(pkt)
		if err != nil {
			m.writeClientErr(cli, srv, err)
			return
		}
		m.handshakeAuthData = hshk.authData
		m.handshakePlugin = hshk.pluginName
		log.Infof("source=%s, phase=%v, version=%v, plugin=%v, flags=%x",
			pkt.Source, m.phase, hshk.protocolVersion, hshk.pluginName, hshk.capabilityFlags)
		// The client will receive the handshake
		// and send a handshake response packet
		m.phase = phaseClientHandshakeResponse
		next()
		return
	}
	var err error
	switch m.phase {
	// The client must respond with handshake response.
	// this phase parses the client packet and the proxy starts
	// the authentication negotiation with the server.
	case phaseClientHandshakeResponse:
		if pkt.Source == types.SourceServer {
			m.writeClientErr(cli, srv, fmt.Errorf("expected handshake response packet from client, pkt=%x", pkt.Encode()))
			return
		}
		err = m.processInitialHandshake(pkt, srv)

	// When the proxy receives a valid authentication response from the server
	// it mutates the packet sequence response and let it flow back to client.
	case phaseHandshakeResponseResult:
		var authOK bool
		authOK, err = m.processHandshakeResponseResult(pkt, srv)
		if authOK {
			log.Debugf("source=%v, phase=%v - authenticated with success", pkt.Source, m.phase)
			pkt.SetSeq(clientSequence)
			m.isAuthenticated = true
			next()
		}
	case phaseAuthSwitchResponseResult:
		err = m.processAuthSwitchResponseResult(pkt, srv)

	case phaseFullAuthPubKeyResponse:
		err = m.processFullAuthPubKeyResponse(pkt, srv)

	case phaseReadAuthResult:
		if pkt.Frame[0] == iOK {
			log.Debugf("phase=%v - authenticated with success", m.phase)
			pkt.SetSeq(clientSequence)
			m.isAuthenticated = true
			next()
			break
		}
		err = m.parseErrorPacket(pkt)

	default:
		err = fmt.Errorf("unknown phase")
	}
	if err != nil {
		log.Errorf("phase=%v - failed processing packet, err=%v, pkt=%x", m.phase, err, pkt.Encode())
		m.writeClientErr(cli, srv, err)
	}
}

// writeClientErr will close the connection with the server and send the error back to client
func (m *authMiddleware) writeClientErr(cli, srv io.WriteCloser, err error) {
	defer srv.Close()
	if data := types.EncodeErrPacket(err, clientSequence); data != nil {
		if _, err := cli.Write(data); err != nil {
			log.Errorf("failed writing error to client, err=%v", err)
		}
	}
}

func (m *authMiddleware) processInitialHandshake(pkt *types.Packet, w io.Writer) error {
	authResp, err := parseAuthData(m.handshakeAuthData, m.authPassword, m.handshakePlugin)
	if err != nil {
		return err
	}
	clientParams, err := parseClientHandshakeResponsePacket(m.authUser, pkt)
	if err != nil {
		return err
	}

	hrpkt := handshakeResponsePacket(authResp, m.handshakePlugin, clientParams)
	log.Debugf("source=%s, phase=%v, sending handshake response", pkt.Source, m.phase)
	hrpkt.SetSeq(1)
	if _, err := w.Write(hrpkt.Encode()); err != nil {
		return err
	}
	m.phase = phaseHandshakeResponseResult
	return nil
}

func (m *authMiddleware) processHandshakeResponseResult(pkt *types.Packet, w io.Writer) (bool, error) {
	authData, newPlugin, err := m.readAuthResult(pkt)
	if err != nil {
		return false, err
	}
	// handle auth plugin switch, if requested
	if newPlugin != "" {
		if authData == nil {
			m.handshakeAuthData = authData
		} else {
			// copy data from read buffer to owned slice
			copy(m.handshakeAuthData, authData)
		}

		m.handshakePlugin = newPlugin
		authResp, err := parseAuthData(authData, m.authPassword, newPlugin)
		if err != nil {
			return false, err
		}
		m.phase = phaseAuthSwitchResponseResult
		// auth switch request
		_, err = w.Write(types.NewPacket(authResp, pkt.Seq()+1).Encode())
		return false, err
	}
	return m.handleAuthResult(authData, m.handshakePlugin, pkt.Seq(), w)
}

func (m *authMiddleware) processAuthSwitchResponseResult(pkt *types.Packet, w io.Writer) error {
	authData, newPlugin, err := m.readAuthResult(pkt)
	if err != nil {
		return err
	}
	if newPlugin != "" {
		// Do not allow to change the auth plugin more than once
		log.Warnf("trying to change auth plugin more than once, plugin=%v", newPlugin)
		return errMalformPkt
	}
	_, err = m.handleAuthResult(authData, m.handshakePlugin, pkt.Seq(), w)
	return err
}

func (m *authMiddleware) processFullAuthPubKeyResponse(pkt *types.Packet, w io.Writer) error {
	if pkt.Frame[0] != iAuthMoreData {
		return fmt.Errorf("malformed packet, expected Protocol::AuthMoreData, got=%X", pkt.Frame[0])
	}
	block, rest := pem.Decode(pkt.Frame[1:])
	if block == nil {
		return fmt.Errorf("no pem data found, data=%v", string(rest))
	}
	pkix, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed parsing pubkey, err=%v", err)
	}
	pubKey := pkix.(*rsa.PublicKey)
	encPass, err := encryptPassword(m.authPassword, m.handshakeAuthData, pubKey)
	if err != nil {
		return fmt.Errorf("failed encrypting password, err=%v", err)
	}
	_, err = w.Write(types.NewPacket(encPass, pkt.Seq()+1).Encode())
	if err != nil {
		return fmt.Errorf("failed writing pubkey response packet, err=%v", err)
	}
	m.phase = phaseReadAuthResult
	return nil
}

func (m *authMiddleware) handleAuthResult(authData []byte, plugin string, seq uint8, w io.Writer) (bool, error) {
	switch plugin {
	// https://insidemysql.com/preparing-your-community-connector-for-mysql-8-part-2-sha256/
	case "caching_sha2_password":
		switch len(authData) {
		case 0:
			log.Debugf("caching sha2 password - authok")
			return true, nil // auth successful
		case 1:
			switch authData[0] {
			case cachingSha2PasswordFastAuthSuccess:
				log.Debugf("caching sha2 fast auth success")
				m.phase = phaseReadAuthResult
				return false, nil
			case cachingSha2PasswordPerformFullAuthentication:
				log.Debugf("processing caching sha2 full authentication")
				// request public key from server
				pkt := types.NewPacket([]byte{cachingSha2PasswordRequestPublicKey}, seq+1)
				m.phase = phaseFullAuthPubKeyResponse
				_, err := w.Write(pkt.Encode())
				return false, err
			default:
				return false, errMalformPkt
			}
		default:
			return false, errMalformPkt
		}

	case "sha256_password":
		return false, fmt.Errorf("authentication plugin sha256_password not supported")
	default:
		return true, nil // auth successful
	}
}

// Error Packet
// http://dev.mysql.com/doc/internals/en/generic-response-packets.html#packet-ERR_Packet
func (m *authMiddleware) parseErrorPacket(pkt *types.Packet) error {
	if pkt.Frame[0] != iERR {
		return errMalformPkt
	}

	// Error Number [16 bit uint]
	errno := binary.LittleEndian.Uint16(pkt.Frame[1:3])
	pos := 3

	var sqlState [5]byte
	// SQL State [optional: # + 5bytes string]
	if pkt.Frame[3] == 0x23 {
		copy(sqlState[:], pkt.Frame[4:9])
		pos = 9
	}

	// Error Message [string]
	errMsg := string(pkt.Frame[pos:])
	return types.NewErrPacket(errno, string(sqlState[:]), errMsg)
}

func (m *authMiddleware) readAuthResult(pkt *types.Packet) ([]byte, string, error) {
	// packet indicator
	switch pkt.Frame[0] {
	case iOK:
		return nil, "", nil
	case iAuthMoreData:
		return pkt.Frame[1:], "", nil
	case iEOF:
		pluginEndIndex := bytes.IndexByte(pkt.Frame, 0x00)
		if pluginEndIndex < 0 {
			return nil, "", errMalformPkt
		}
		plugin := string(pkt.Frame[1:pluginEndIndex])
		authData := pkt.Frame[pluginEndIndex+1:]
		return authData, plugin, nil
	case iERR:
		return nil, "", m.parseErrorPacket(pkt)
	default:
		return nil, "", errMalformPkt
	}
}
