package proxy

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	mysqlauth "github.com/hoophq/libhoop/v2/codec/mysql/auth"
)

const (
	mysqlClientConnectWithDB              = mysqlauth.ClientConnectWithDB
	mysqlClientSSL                        = mysqlauth.ClientSSL
	mysqlClientSecureConnection           = mysqlauth.ClientSecureConnection
	mysqlClientPluginAuth                 = mysqlauth.ClientPluginAuth
	mysqlClientPluginAuthLenencClientData = mysqlauth.ClientPluginAuthLenencData
	mysqlMaxPacketPayload                 = (1 << 24) - 1
	mysqlMaxHandshakeMessageSize          = mysqlMaxPacketPayload + (1 << 20)
	mysqlHandshakeReadChunkSize           = 32 << 10
	mysqlMaxConcurrentHandshakes          = 8
	mysqlMaxAuthRounds                    = 32
	mysqlProtocol41HeaderLen              = mysqlauth.ResponseHeaderLen
	mysqlMinRSACiphertextSize             = 128
	mysqlAuthMoreData                     = mysqlauth.AuthMoreData
	mysqlAuthSwitchRequest                = mysqlauth.EOF
	mysqlOKPacket                         = mysqlauth.OK
	mysqlERRPacket                        = mysqlauth.ERR
	mysqlCachingSHA2                      = mysqlauth.CachingSHA2Password
	mysqlSHA256Password                   = mysqlauth.SHA256Password
	mysqlDirectRSAErrorMessage            = "direct RSA authentication requires the client to pin the relay authentication public key from mysql_auth_key_file; the pinned key is missing or does not match"
)

var errMySQLDirectRSA = errors.New(mysqlDirectRSAErrorMessage)

type mysqlHandshakeInspector func(inspect.Direction, []byte) ([]byte, error)

type mysqlAuthBridge struct {
	key        *rsa.PrivateKey
	publicPEM  []byte
	configured bool
}

func newMySQLAuthBridge(key *rsa.PrivateKey) (*mysqlAuthBridge, error) {
	configured := key != nil
	if key == nil {
		var err error
		key, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, fmt.Errorf("generate MySQL authentication key: %w", err)
		}
	} else if err := key.Validate(); err != nil {
		return nil, fmt.Errorf("validate MySQL authentication key: %w", err)
	}
	publicPEM, err := mysqlauth.EncodePublicKeyPEM(&key.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("encode MySQL authentication key: %w", err)
	}
	return &mysqlAuthBridge{key: key, configured: configured, publicPEM: publicPEM}, nil
}

type mysqlPacket struct {
	seq     byte
	nextSeq byte
	payload []byte
}

func readMySQLMessage(conn net.Conn, maxPacketPayload, maxMessageSize int) (mysqlPacket, error) {
	if maxPacketPayload <= 0 || maxMessageSize < 0 {
		return mysqlPacket{}, errors.New("invalid MySQL message limits")
	}

	var (
		header   [4]byte
		payload  []byte
		startSeq byte
		nextSeq  byte
		first    = true
	)
	scratch := make([]byte, mysqlHandshakeReadChunkSize)
	for {
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return mysqlPacket{}, err
		}
		length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
		if length > maxPacketPayload {
			return mysqlPacket{}, fmt.Errorf("MySQL packet is %d bytes, maximum is %d", length, maxPacketPayload)
		}
		if first {
			startSeq = header[3]
			nextSeq = startSeq
			first = false
		}
		if header[3] != nextSeq {
			return mysqlPacket{}, fmt.Errorf("MySQL continuation sequence is %d, want %d", header[3], nextSeq)
		}
		nextSeq++
		if length > maxMessageSize-len(payload) {
			return mysqlPacket{}, fmt.Errorf("MySQL handshake message exceeds %d bytes", maxMessageSize)
		}

		remaining := length
		for remaining > 0 {
			n := min(remaining, len(scratch))
			if _, err := io.ReadFull(conn, scratch[:n]); err != nil {
				return mysqlPacket{}, err
			}
			payload = append(payload, scratch[:n]...)
			remaining -= n
		}
		if length < maxPacketPayload {
			return mysqlPacket{seq: startSeq, nextSeq: nextSeq, payload: payload}, nil
		}
	}
}

func readMySQLHandshakeMessage(conn net.Conn) (mysqlPacket, error) {
	return readMySQLMessage(conn, mysqlMaxPacketPayload, mysqlMaxHandshakeMessageSize)
}

// readMySQLHandshakeMessageSeq reads one message and checks it carries the
// sequence id the exchange is at.
func readMySQLHandshakeMessageSeq(conn net.Conn, want byte, what string) (mysqlPacket, error) {
	msg, err := readMySQLHandshakeMessage(conn)
	if err != nil {
		return mysqlPacket{}, fmt.Errorf("read MySQL %s: %w", what, err)
	}
	if msg.seq != want {
		return mysqlPacket{}, fmt.Errorf("MySQL %s sequence is %d, want %d", what, msg.seq, want)
	}
	return msg, nil
}

func frameMySQLHandshakePacket(seq byte, payload []byte) []byte {
	out := make([]byte, 4+len(payload))
	out[0] = byte(len(payload))
	out[1] = byte(len(payload) >> 8)
	out[2] = byte(len(payload) >> 16)
	out[3] = seq
	copy(out[4:], payload)
	return out
}

func writeAll(conn io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
		data = data[n:]
	}
	return nil
}

func writeMySQLMessage(
	conn io.Writer,
	seq byte,
	payload []byte,
	maxPacketPayload int,
	inspectPacket mysqlHandshakeInspector,
	dir inspect.Direction,
) (byte, error) {
	if maxPacketPayload <= 0 {
		return seq, errors.New("invalid MySQL packet limit")
	}

	wroteInspected := false
	for {
		chunkLen := min(len(payload), maxPacketPayload)
		chunk := payload[:chunkLen]
		if inspectPacket == nil {
			var header [4]byte
			header[0] = byte(chunkLen)
			header[1] = byte(chunkLen >> 8)
			header[2] = byte(chunkLen >> 16)
			header[3] = seq
			if err := writeAll(conn, header[:]); err != nil {
				return seq, err
			}
			if err := writeAll(conn, chunk); err != nil {
				return seq, err
			}
		} else {
			out, err := inspectPacket(dir, frameMySQLHandshakePacket(seq, chunk))
			if err != nil {
				return seq, err
			}
			if len(out) > 0 {
				if err := writeAll(conn, out); err != nil {
					return seq, err
				}
				wroteInspected = true
			}
		}

		seq++
		payload = payload[chunkLen:]
		if chunkLen < maxPacketPayload {
			if inspectPacket != nil && !wroteInspected {
				return seq, errors.New("MySQL handshake inspector held a complete message")
			}
			return seq, nil
		}
	}
}

func writeMySQLHandshakeMessage(
	conn io.Writer,
	seq byte,
	payload []byte,
	inspectPacket mysqlHandshakeInspector,
	dir inspect.Direction,
) (byte, error) {
	return writeMySQLMessage(conn, seq, payload, mysqlMaxPacketPayload, inspectPacket, dir)
}

// inspectMySQLClientMessage feeds a client message to the gate without
// forwarding it: the relay decides what goes upstream.
func inspectMySQLClientMessage(msg mysqlPacket, inspectPacket mysqlHandshakeInspector) error {
	_, err := writeMySQLMessage(io.Discard, msg.seq, msg.payload, mysqlMaxPacketPayload, inspectPacket, inspect.FromClient)
	return err
}

// mysqlGreetingCapabilityOffset is where the lower capability bytes sit in
// a greeting: after the version string, the thread id, the first scramble
// part and its filler.
func mysqlGreetingCapabilityOffset(h mysqlauth.Handshake) int {
	return 1 + len(h.ServerVersion) + 1 + 4 + 8 + 1
}

func mysqlRequestsPublicKey(plugin string, payload []byte) bool {
	switch plugin {
	case mysqlCachingSHA2:
		return bytes.Equal(payload, []byte{mysqlauth.CachingSHA2RequestPublicKey})
	case mysqlSHA256Password:
		return bytes.Equal(payload, []byte{mysqlauth.SHA256RequestPublicKey})
	default:
		return false
	}
}

type mysqlHandshakeAuthResponse struct {
	plugin      string
	auth        []byte
	lengthStart int
	authStart   int
	authEnd     int
	suffixStart int
	encoding    byte
}

const (
	mysqlAuthEncodingLenenc byte = iota
	mysqlAuthEncodingByte
	mysqlAuthEncodingNUL
)

func readMySQLLengthEncodedInt(data []byte) (uint64, int, error) {
	if len(data) == 0 {
		return 0, 0, errors.New("missing length-encoded integer")
	}
	switch data[0] {
	case 0xfc:
		if len(data) < 3 {
			return 0, 0, errors.New("truncated two-byte length-encoded integer")
		}
		return uint64(binary.LittleEndian.Uint16(data[1:3])), 3, nil
	case 0xfd:
		if len(data) < 4 {
			return 0, 0, errors.New("truncated three-byte length-encoded integer")
		}
		return uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16, 4, nil
	case 0xfe:
		if len(data) < 9 {
			return 0, 0, errors.New("truncated eight-byte length-encoded integer")
		}
		return binary.LittleEndian.Uint64(data[1:9]), 9, nil
	case 0xfb:
		return 0, 0, errors.New("NULL is not a valid authentication response length")
	default:
		return uint64(data[0]), 1, nil
	}
}

func appendMySQLLengthEncodedInt(dst []byte, n uint64) []byte {
	switch {
	case n < 0xfb:
		return append(dst, byte(n))
	case n <= 0xffff:
		dst = append(dst, 0xfc)
		return binary.LittleEndian.AppendUint16(dst, uint16(n))
	case n <= 0xffffff:
		return append(dst, 0xfd, byte(n), byte(n>>8), byte(n>>16))
	default:
		dst = append(dst, 0xfe)
		return binary.LittleEndian.AppendUint64(dst, n)
	}
}

// parseMySQLHandshakeAuthResponse locates the auth response inside a
// HandshakeResponse41 so rewriteMySQLHandshakeRSAResponse can splice a
// replacement in and leave every other byte of the client's packet alone.
func parseMySQLHandshakeAuthResponse(payload []byte, fallbackPlugin string) (mysqlHandshakeAuthResponse, error) {
	if len(payload) < mysqlProtocol41HeaderLen {
		return mysqlHandshakeAuthResponse{}, fmt.Errorf(
			"MySQL HandshakeResponse41 is %d bytes, want at least %d",
			len(payload), mysqlProtocol41HeaderLen,
		)
	}
	flags := binary.LittleEndian.Uint32(payload[:4])
	off := mysqlProtocol41HeaderLen
	usernameEnd := bytes.IndexByte(payload[off:], 0)
	if usernameEnd < 0 {
		return mysqlHandshakeAuthResponse{}, errors.New("MySQL HandshakeResponse41 has no username terminator")
	}
	off += usernameEnd + 1

	response := mysqlHandshakeAuthResponse{
		plugin:      fallbackPlugin,
		lengthStart: off,
	}
	switch {
	case flags&mysqlClientPluginAuthLenencClientData != 0:
		authLen, prefixLen, err := readMySQLLengthEncodedInt(payload[off:])
		if err != nil {
			return mysqlHandshakeAuthResponse{}, fmt.Errorf("read MySQL authentication response length: %w", err)
		}
		if authLen > uint64(len(payload)) {
			return mysqlHandshakeAuthResponse{}, errors.New("MySQL authentication response length overflows the packet")
		}
		response.encoding = mysqlAuthEncodingLenenc
		response.authStart = off + prefixLen
		response.authEnd = response.authStart + int(authLen)
		response.suffixStart = response.authEnd
	case flags&mysqlClientSecureConnection != 0:
		if off >= len(payload) {
			return mysqlHandshakeAuthResponse{}, errors.New("MySQL HandshakeResponse41 has no authentication response length")
		}
		response.encoding = mysqlAuthEncodingByte
		response.authStart = off + 1
		response.authEnd = response.authStart + int(payload[off])
		response.suffixStart = response.authEnd
	default:
		authEnd := bytes.IndexByte(payload[off:], 0)
		if authEnd < 0 {
			return mysqlHandshakeAuthResponse{}, errors.New("MySQL HandshakeResponse41 has no authentication response terminator")
		}
		response.encoding = mysqlAuthEncodingNUL
		response.authStart = off
		response.authEnd = off + authEnd
		response.suffixStart = response.authEnd + 1
	}
	if response.authEnd > len(payload) {
		return mysqlHandshakeAuthResponse{}, errors.New("MySQL HandshakeResponse41 authentication response is truncated")
	}
	response.auth = payload[response.authStart:response.authEnd]

	off = response.suffixStart
	if flags&mysqlClientConnectWithDB != 0 {
		databaseEnd := bytes.IndexByte(payload[off:], 0)
		if databaseEnd < 0 {
			return mysqlHandshakeAuthResponse{}, errors.New("MySQL HandshakeResponse41 has no database terminator")
		}
		off += databaseEnd + 1
	}
	if flags&mysqlClientPluginAuth != 0 {
		pluginEnd := bytes.IndexByte(payload[off:], 0)
		if pluginEnd < 0 {
			return mysqlHandshakeAuthResponse{}, errors.New("MySQL HandshakeResponse41 has no authentication plugin terminator")
		}
		response.plugin = string(payload[off : off+pluginEnd])
	}
	return response, nil
}

func rewriteMySQLHandshakeRSAResponse(
	payload []byte,
	fallbackPlugin string,
	scramble []byte,
	bridge *mysqlAuthBridge,
) ([]byte, bool, error) {
	response, err := parseMySQLHandshakeAuthResponse(payload, fallbackPlugin)
	if err != nil {
		return nil, false, err
	}
	directRSA := len(response.auth) == bridge.key.Size() ||
		len(response.auth) >= mysqlMinRSACiphertextSize ||
		(len(response.auth) > 0 && response.auth[len(response.auth)-1] != 0)
	if response.plugin != mysqlSHA256Password || len(response.auth) <= 1 || !directRSA {
		return payload, false, nil
	}
	if !bridge.configured {
		return nil, false, errMySQLDirectRSA
	}
	password, err := bridge.decryptPassword(response.plugin, response.auth, scramble)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", errMySQLDirectRSA, err)
	}
	defer clear(password)

	out := make([]byte, 0, len(payload)-len(response.auth)+len(password)+9)
	out = append(out, payload[:response.lengthStart]...)
	switch response.encoding {
	case mysqlAuthEncodingLenenc:
		out = appendMySQLLengthEncodedInt(out, uint64(len(password)))
		out = append(out, password...)
	case mysqlAuthEncodingByte:
		if len(password) > 255 {
			return nil, false, errors.New("decrypted MySQL authentication response exceeds one-byte length")
		}
		out = append(out, byte(len(password)))
		out = append(out, password...)
	case mysqlAuthEncodingNUL:
		out = append(out, bytes.TrimSuffix(password, []byte{0})...)
		out = append(out, 0)
	default:
		return nil, false, errors.New("unknown MySQL authentication response encoding")
	}
	out = append(out, payload[response.suffixStart:]...)
	return out, true, nil
}

func mysqlIsDirectRSAResponse(
	plugin string,
	serverPayload, clientPayload []byte,
	bridge *mysqlAuthBridge,
) bool {
	if len(clientPayload) == 0 || mysqlRequestsPublicKey(plugin, clientPayload) {
		return false
	}
	directRSA := len(clientPayload) == bridge.key.Size() ||
		len(clientPayload) >= mysqlMinRSACiphertextSize ||
		clientPayload[len(clientPayload)-1] != 0
	if !directRSA {
		return false
	}
	switch plugin {
	case mysqlCachingSHA2:
		return bytes.Equal(serverPayload, []byte{mysqlAuthMoreData, mysqlauth.CachingSHA2PerformFullAuth})
	case mysqlSHA256Password:
		return true
	default:
		return false
	}
}

// writeMySQLAuthError sends ERR 1045 to the client and returns an error
// carrying message, so the caller reports what the client was told.
func writeMySQLAuthError(
	client net.Conn,
	seq byte,
	message string,
	inspectPacket mysqlHandshakeInspector,
) error {
	payload := mysqlauth.EncodeERR(mysqlauth.ErrAccessDenied, mysqlauth.SQLStateAccessDenied, message)
	if _, err := writeMySQLHandshakeMessage(client, seq, payload, inspectPacket, inspect.FromServer); err != nil {
		return fmt.Errorf("%s; write MySQL authentication error: %w", message, err)
	}
	return errors.New(message)
}

// decryptPassword returns the NUL-terminated password a client encrypted
// to the relay's key, in the form the upstream expects over TLS.
func (b *mysqlAuthBridge) decryptPassword(plugin string, ciphertext, scramble []byte) ([]byte, error) {
	password, err := mysqlauth.DecryptPasswordRSA(ciphertext, scramble, b.key)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", plugin, err)
	}
	return append(password, 0), nil
}

// mysqlAuthRelay bridges the authentication packets of one connection after
// the handshake response went upstream: the upstream's packets go to the
// client and the client's answers go upstream, until the upstream says OK
// or ERR.
//
// With a secure upstream leg the relay also stands in for the upstream's
// RSA key: it serves its own public key to a client on a plaintext leg,
// decrypts the password, and forwards it in the clear under TLS, as the
// upstream expects from a client it considers secure. Without one the RSA
// exchange is relayed as it is, against the upstream's own key.
type mysqlAuthRelay struct {
	client, upstream net.Conn
	bridge           *mysqlAuthBridge
	inspectPacket    mysqlHandshakeInspector
	plugin           string
	scramble         []byte
	downNext, upNext byte
	upstreamSecure   bool
}

func (r *mysqlAuthRelay) run() error {
	for range mysqlMaxAuthRounds {
		serverMessage, err := readMySQLHandshakeMessageSeq(r.upstream, r.upNext, "upstream authentication")
		if err != nil {
			return err
		}
		r.upNext = serverMessage.nextSeq
		if p, s, ok := mysqlauth.ParseAuthSwitchRequest(serverMessage.payload); ok {
			r.plugin, r.scramble = p, s
		}

		r.downNext, err = writeMySQLHandshakeMessage(
			r.client, r.downNext, serverMessage.payload, r.inspectPacket, inspect.FromServer,
		)
		if err != nil {
			return fmt.Errorf("write MySQL authentication response: %w", err)
		}

		if len(serverMessage.payload) == 0 {
			return errors.New("MySQL upstream sent an empty authentication packet")
		}
		switch serverMessage.payload[0] {
		case mysqlOKPacket, mysqlERRPacket:
			return nil
		}
		if bytes.Equal(serverMessage.payload, []byte{mysqlAuthMoreData, mysqlauth.CachingSHA2FastAuthSuccess}) {
			continue
		}

		clientMessage, err := readMySQLHandshakeMessageSeq(r.client, r.downNext, "client authentication")
		if err != nil {
			return err
		}
		if err := inspectMySQLClientMessage(clientMessage, r.inspectPacket); err != nil {
			return err
		}
		r.downNext = clientMessage.nextSeq

		if r.upstreamSecure && mysqlRequestsPublicKey(r.plugin, clientMessage.payload) {
			publicKeyPayload := append([]byte{mysqlAuthMoreData}, r.bridge.publicPEM...)
			r.downNext, err = writeMySQLHandshakeMessage(
				r.client, r.downNext, publicKeyPayload, r.inspectPacket, inspect.FromServer,
			)
			if err != nil {
				return fmt.Errorf("write MySQL authentication public key: %w", err)
			}

			encryptedMessage, err := readMySQLHandshakeMessageSeq(r.client, r.downNext, "encrypted authentication")
			if err != nil {
				return err
			}
			if err := inspectMySQLClientMessage(encryptedMessage, r.inspectPacket); err != nil {
				return err
			}
			r.downNext = encryptedMessage.nextSeq

			password, err := r.bridge.decryptPassword(r.plugin, encryptedMessage.payload, r.scramble)
			if err != nil {
				return err
			}
			r.upNext, err = writeMySQLHandshakeMessage(r.upstream, r.upNext, password, nil, inspect.FromClient)
			clear(password)
			if err != nil {
				return fmt.Errorf("write MySQL full authentication response: %w", err)
			}
			continue
		}

		upstreamPayload := clientMessage.payload
		var password []byte
		if r.upstreamSecure && mysqlIsDirectRSAResponse(r.plugin, serverMessage.payload, clientMessage.payload, r.bridge) {
			if !r.bridge.configured {
				return writeMySQLAuthError(r.client, r.downNext, mysqlDirectRSAErrorMessage, r.inspectPacket)
			}
			password, err = r.bridge.decryptPassword(r.plugin, clientMessage.payload, r.scramble)
			if err != nil {
				return writeMySQLAuthError(r.client, r.downNext, mysqlDirectRSAErrorMessage, r.inspectPacket)
			}
			upstreamPayload = password
		}

		r.upNext, err = writeMySQLHandshakeMessage(r.upstream, r.upNext, upstreamPayload, nil, inspect.FromClient)
		clear(password)
		if err != nil {
			return fmt.Errorf("write MySQL authentication response upstream: %w", err)
		}
	}

	return fmt.Errorf("MySQL authentication exceeded %d round trips", mysqlMaxAuthRounds)
}

// negotiateMySQLUpstreamTLS completes the server-first MySQL TLS and
// authentication exchange before the ordinary bidirectional pumps start. The
// client remains on plaintext, while the upstream receives an SSLRequest and
// all subsequent credentials over TLS.
func negotiateMySQLUpstreamTLS(
	client, upstream net.Conn,
	upstreamAddr string,
	tlsCfg *tls.Config,
	timeout time.Duration,
	bridge *mysqlAuthBridge,
	inspectPacket mysqlHandshakeInspector,
) (net.Conn, net.Conn, error) {
	if timeout > 0 {
		deadline := time.Now().Add(timeout)
		if err := client.SetDeadline(deadline); err != nil {
			return nil, nil, err
		}
		if err := upstream.SetDeadline(deadline); err != nil {
			return nil, nil, err
		}
	}

	greetingMessage, err := readMySQLHandshakeMessageSeq(upstream, 0, "greeting")
	if err != nil {
		return nil, nil, err
	}
	greeting, err := mysqlauth.ParseHandshake(greetingMessage.payload)
	if err != nil {
		return nil, nil, err
	}
	if greeting.Capabilities&mysqlClientSSL == 0 {
		return nil, nil, errors.New("MySQL upstream does not advertise CLIENT_SSL")
	}
	if len(greeting.Scramble) == 0 {
		return nil, nil, errors.New("MySQL greeting contains no authentication scramble")
	}

	// The client's copy of the greeting is the upstream's, byte for byte,
	// with CLIENT_SSL cleared in place so the client stays on plaintext.
	clientGreeting := append([]byte(nil), greetingMessage.payload...)
	capOff := mysqlGreetingCapabilityOffset(greeting)
	lower := binary.LittleEndian.Uint16(clientGreeting[capOff : capOff+2])
	binary.LittleEndian.PutUint16(clientGreeting[capOff:capOff+2], lower&^uint16(mysqlClientSSL))
	downNext, err := writeMySQLHandshakeMessage(
		client, 0, clientGreeting, inspectPacket, inspect.FromServer,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("write MySQL greeting: %w", err)
	}

	responseMessage, err := readMySQLHandshakeMessageSeq(client, downNext, "handshake response")
	if err != nil {
		return nil, nil, err
	}
	if len(responseMessage.payload) < mysqlProtocol41HeaderLen {
		return nil, nil, fmt.Errorf("MySQL HandshakeResponse41 is %d bytes, want at least %d", len(responseMessage.payload), mysqlProtocol41HeaderLen)
	}
	if err := inspectMySQLClientMessage(responseMessage, inspectPacket); err != nil {
		return nil, nil, err
	}
	downNext = responseMessage.nextSeq
	rewrittenResponse, rewritten, err := rewriteMySQLHandshakeRSAResponse(
		responseMessage.payload, greeting.Plugin, greeting.Scramble, bridge,
	)
	if err != nil {
		if errors.Is(err, errMySQLDirectRSA) {
			return nil, nil, writeMySQLAuthError(client, downNext, mysqlDirectRSAErrorMessage, inspectPacket)
		}
		return nil, nil, err
	}
	if rewritten {
		responseMessage.payload = rewrittenResponse
	}

	sslRequest := append([]byte(nil), responseMessage.payload[:mysqlProtocol41HeaderLen]...)
	flags := binary.LittleEndian.Uint32(sslRequest[:4]) | mysqlClientSSL
	binary.LittleEndian.PutUint32(sslRequest[:4], flags)
	upNext, err := writeMySQLHandshakeMessage(upstream, greetingMessage.nextSeq, sslRequest, nil, inspect.FromClient)
	if err != nil {
		return nil, nil, fmt.Errorf("write MySQL SSLRequest: %w", err)
	}

	tlsConn, err := startTLS(upstream, upstreamAddr, inspect.MySQL, tlsCfg)
	if err != nil {
		return nil, nil, err
	}
	upstream = tlsConn
	upNext, err = writeMySQLHandshakeMessage(upstream, upNext, responseMessage.payload, nil, inspect.FromClient)
	if err != nil {
		return nil, nil, fmt.Errorf("write encrypted MySQL handshake response: %w", err)
	}

	relay := &mysqlAuthRelay{
		client:         client,
		upstream:       upstream,
		bridge:         bridge,
		inspectPacket:  inspectPacket,
		plugin:         greeting.Plugin,
		scramble:       greeting.Scramble,
		downNext:       downNext,
		upNext:         upNext,
		upstreamSecure: true,
	}
	if err := relay.run(); err != nil {
		return nil, nil, err
	}
	_ = client.SetDeadline(time.Time{})
	_ = upstream.SetDeadline(time.Time{})
	return client, upstream, nil
}
