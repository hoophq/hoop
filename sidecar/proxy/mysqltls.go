package proxy

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
)

const (
	mysqlClientSSL               uint32 = 1 << 11
	mysqlMaxPacketPayload               = (1 << 24) - 1
	mysqlMaxHandshakeMessageSize        = mysqlMaxPacketPayload + (1 << 20)
	mysqlHandshakeReadChunkSize         = 32 << 10
	mysqlMaxConcurrentHandshakes        = 8
	mysqlMaxAuthRounds                  = 32
	mysqlProtocol41HeaderLen            = 32
	mysqlAuthMoreData            byte   = 0x01
	mysqlAuthSwitchRequest       byte   = 0xfe
	mysqlOKPacket                byte   = 0x00
	mysqlERRPacket               byte   = 0xff
	mysqlCachingSHA2                    = "caching_sha2_password"
	mysqlSHA256Password                 = "sha256_password"
)

type mysqlHandshakeInspector func(inspect.Direction, []byte) ([]byte, error)

type mysqlAuthBridge struct {
	key       *rsa.PrivateKey
	publicPEM []byte
}

func newMySQLAuthBridge() (*mysqlAuthBridge, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate MySQL authentication key: %w", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("encode MySQL authentication key: %w", err)
	}
	return &mysqlAuthBridge{
		key: key,
		publicPEM: pem.EncodeToMemory(&pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: der,
		}),
	}, nil
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

type mysqlGreeting struct {
	capabilityOffset int
	capabilities     uint32
	scramble         []byte
	plugin           string
}

func parseMySQLGreeting(payload []byte) (mysqlGreeting, error) {
	if len(payload) < 1 || payload[0] != 10 {
		return mysqlGreeting{}, errors.New("MySQL upstream did not send a protocol 10 greeting")
	}
	versionEnd := bytes.IndexByte(payload[1:], 0)
	if versionEnd < 0 {
		return mysqlGreeting{}, errors.New("MySQL greeting has no server-version terminator")
	}
	off := 1 + versionEnd + 1
	if len(payload) < off+4+8+1+2 {
		return mysqlGreeting{}, errors.New("MySQL greeting is truncated before capability flags")
	}
	off += 4
	part1 := append([]byte(nil), payload[off:off+8]...)
	off += 8 + 1
	capOffset := off
	lower := binary.LittleEndian.Uint16(payload[off : off+2])
	off += 2
	if len(payload) == off {
		return mysqlGreeting{capabilityOffset: capOffset, capabilities: uint32(lower), scramble: part1}, nil
	}
	if len(payload) < off+1+2+2+1+10 {
		return mysqlGreeting{}, errors.New("MySQL greeting is truncated after capability flags")
	}
	off += 1 + 2
	upper := binary.LittleEndian.Uint16(payload[off : off+2])
	off += 2
	authLen := int(payload[off])
	off++
	off += 10

	part2Len := 13
	if authLen > 8 && authLen-8 > part2Len {
		part2Len = authLen - 8
	}
	if part2Len > len(payload)-off {
		part2Len = len(payload) - off
	}
	part2 := bytes.TrimSuffix(payload[off:off+part2Len], []byte{0})
	scramble := append(part1, part2...)
	if authLen > 0 && len(scramble) > authLen-1 {
		scramble = scramble[:authLen-1]
	}
	off += part2Len
	plugin := ""
	if off < len(payload) {
		name := payload[off:]
		if end := bytes.IndexByte(name, 0); end >= 0 {
			name = name[:end]
		}
		plugin = string(name)
	}

	return mysqlGreeting{
		capabilityOffset: capOffset,
		capabilities:     uint32(lower) | uint32(upper)<<16,
		scramble:         scramble,
		plugin:           plugin,
	}, nil
}

func mysqlAuthSwitch(payload []byte) (plugin string, scramble []byte, ok bool) {
	if len(payload) < 3 || payload[0] != mysqlAuthSwitchRequest {
		return "", nil, false
	}
	nameEnd := bytes.IndexByte(payload[1:], 0)
	if nameEnd < 0 {
		return "", nil, false
	}
	nameEnd++
	plugin = string(payload[1:nameEnd])
	scramble = bytes.TrimSuffix(payload[nameEnd+1:], []byte{0})
	return plugin, scramble, true
}

func mysqlRequestsPublicKey(plugin string, payload []byte) bool {
	switch plugin {
	case mysqlCachingSHA2:
		return bytes.Equal(payload, []byte{0x02})
	case mysqlSHA256Password:
		return bytes.Equal(payload, []byte{0x01})
	default:
		return false
	}
}

func (b *mysqlAuthBridge) decryptPassword(plugin string, ciphertext, scramble []byte) ([]byte, error) {
	if len(scramble) == 0 {
		return nil, fmt.Errorf("%s RSA authentication has no scramble", plugin)
	}
	if len(ciphertext) != b.key.Size() {
		return nil, fmt.Errorf("%s encrypted response is %d bytes, want %d", plugin, len(ciphertext), b.key.Size())
	}
	plain, err := rsa.DecryptOAEP(sha1.New(), rand.Reader, b.key, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt %s response: %w", plugin, err)
	}
	for i := range plain {
		plain[i] ^= scramble[i%len(scramble)]
	}
	if len(plain) == 0 || plain[len(plain)-1] != 0 {
		clear(plain)
		return nil, fmt.Errorf("%s response is not NUL-terminated", plugin)
	}
	return plain, nil
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

	greetingMessage, err := readMySQLHandshakeMessage(upstream)
	if err != nil {
		return nil, nil, fmt.Errorf("read MySQL greeting: %w", err)
	}
	if greetingMessage.seq != 0 {
		return nil, nil, fmt.Errorf("MySQL greeting sequence is %d, want 0", greetingMessage.seq)
	}
	greeting, err := parseMySQLGreeting(greetingMessage.payload)
	if err != nil {
		return nil, nil, err
	}
	if greeting.capabilities&mysqlClientSSL == 0 {
		return nil, nil, errors.New("MySQL upstream does not advertise CLIENT_SSL")
	}
	if len(greeting.scramble) == 0 {
		return nil, nil, errors.New("MySQL greeting contains no authentication scramble")
	}

	clientGreeting := append([]byte(nil), greetingMessage.payload...)
	lower := binary.LittleEndian.Uint16(clientGreeting[greeting.capabilityOffset : greeting.capabilityOffset+2])
	binary.LittleEndian.PutUint16(clientGreeting[greeting.capabilityOffset:greeting.capabilityOffset+2], lower&^uint16(mysqlClientSSL))
	downNext, err := writeMySQLHandshakeMessage(
		client, 0, clientGreeting, inspectPacket, inspect.FromServer,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("write MySQL greeting: %w", err)
	}

	responseMessage, err := readMySQLHandshakeMessage(client)
	if err != nil {
		return nil, nil, fmt.Errorf("read MySQL handshake response: %w", err)
	}
	if responseMessage.seq != downNext {
		return nil, nil, fmt.Errorf("MySQL handshake response sequence is %d, want %d", responseMessage.seq, downNext)
	}
	if len(responseMessage.payload) < mysqlProtocol41HeaderLen {
		return nil, nil, fmt.Errorf("MySQL HandshakeResponse41 is %d bytes, want at least %d", len(responseMessage.payload), mysqlProtocol41HeaderLen)
	}
	if _, err := writeMySQLMessage(
		io.Discard,
		responseMessage.seq,
		responseMessage.payload,
		mysqlMaxPacketPayload,
		inspectPacket,
		inspect.FromClient,
	); err != nil {
		return nil, nil, err
	}
	downNext = responseMessage.nextSeq

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

	plugin := greeting.plugin
	scramble := greeting.scramble
	for range mysqlMaxAuthRounds {
		serverMessage, err := readMySQLHandshakeMessage(upstream)
		if err != nil {
			return nil, nil, fmt.Errorf("read MySQL authentication response: %w", err)
		}
		if serverMessage.seq != upNext {
			return nil, nil, fmt.Errorf("MySQL upstream authentication sequence is %d, want %d", serverMessage.seq, upNext)
		}
		upNext = serverMessage.nextSeq
		if p, s, ok := mysqlAuthSwitch(serverMessage.payload); ok {
			plugin, scramble = p, s
		}

		downNext, err = writeMySQLHandshakeMessage(
			client, downNext, serverMessage.payload, inspectPacket, inspect.FromServer,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("write MySQL authentication response: %w", err)
		}

		if len(serverMessage.payload) == 0 {
			return nil, nil, errors.New("MySQL upstream sent an empty authentication packet")
		}
		switch serverMessage.payload[0] {
		case mysqlOKPacket, mysqlERRPacket:
			_ = client.SetDeadline(time.Time{})
			_ = upstream.SetDeadline(time.Time{})
			return client, upstream, nil
		}
		if bytes.Equal(serverMessage.payload, []byte{mysqlAuthMoreData, 0x03}) {
			continue
		}

		clientMessage, err := readMySQLHandshakeMessage(client)
		if err != nil {
			return nil, nil, fmt.Errorf("read MySQL authentication response from client: %w", err)
		}
		if clientMessage.seq != downNext {
			return nil, nil, fmt.Errorf("MySQL client authentication sequence is %d, want %d", clientMessage.seq, downNext)
		}
		if _, err := writeMySQLMessage(
			io.Discard,
			clientMessage.seq,
			clientMessage.payload,
			mysqlMaxPacketPayload,
			inspectPacket,
			inspect.FromClient,
		); err != nil {
			return nil, nil, err
		}
		downNext = clientMessage.nextSeq

		if mysqlRequestsPublicKey(plugin, clientMessage.payload) {
			publicKeyPayload := append([]byte{mysqlAuthMoreData}, bridge.publicPEM...)
			downNext, err = writeMySQLHandshakeMessage(
				client, downNext, publicKeyPayload, inspectPacket, inspect.FromServer,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("write MySQL authentication public key: %w", err)
			}

			encryptedMessage, err := readMySQLHandshakeMessage(client)
			if err != nil {
				return nil, nil, fmt.Errorf("read encrypted MySQL authentication response: %w", err)
			}
			if encryptedMessage.seq != downNext {
				return nil, nil, fmt.Errorf("MySQL encrypted authentication sequence is %d, want %d", encryptedMessage.seq, downNext)
			}
			if _, err := writeMySQLMessage(
				io.Discard,
				encryptedMessage.seq,
				encryptedMessage.payload,
				mysqlMaxPacketPayload,
				inspectPacket,
				inspect.FromClient,
			); err != nil {
				return nil, nil, err
			}
			downNext = encryptedMessage.nextSeq

			password, err := bridge.decryptPassword(plugin, encryptedMessage.payload, scramble)
			if err != nil {
				return nil, nil, err
			}
			upNext, err = writeMySQLHandshakeMessage(upstream, upNext, password, nil, inspect.FromClient)
			clear(password)
			if err != nil {
				return nil, nil, fmt.Errorf("write MySQL full authentication response: %w", err)
			}
			continue
		}

		upNext, err = writeMySQLHandshakeMessage(upstream, upNext, clientMessage.payload, nil, inspect.FromClient)
		if err != nil {
			return nil, nil, fmt.Errorf("write MySQL authentication response upstream: %w", err)
		}
	}

	return nil, nil, fmt.Errorf("MySQL authentication exceeded %d round trips", mysqlMaxAuthRounds)
}
