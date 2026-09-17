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
	mysqlClientSSL           uint32 = 1 << 11
	mysqlMaxHandshakePayload        = (1 << 24) - 1
	mysqlMaxAuthRounds              = 32
	mysqlProtocol41HeaderLen        = 32
	mysqlAuthMoreData        byte   = 0x01
	mysqlAuthSwitchRequest   byte   = 0xfe
	mysqlOKPacket            byte   = 0x00
	mysqlERRPacket           byte   = 0xff
	mysqlCachingSHA2                = "caching_sha2_password"
	mysqlSHA256Password             = "sha256_password"
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
	payload []byte
}

func readMySQLHandshakePacket(conn net.Conn) (mysqlPacket, error) {
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return mysqlPacket{}, err
	}
	length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	if length > mysqlMaxHandshakePayload {
		return mysqlPacket{}, fmt.Errorf("MySQL handshake packet is %d bytes, maximum is %d", length, mysqlMaxHandshakePayload)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return mysqlPacket{}, err
	}
	return mysqlPacket{seq: header[3], payload: payload}, nil
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

func writeAll(conn net.Conn, data []byte) error {
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

func inspectMySQLHandshake(inspectPacket mysqlHandshakeInspector, dir inspect.Direction, frame []byte) ([]byte, error) {
	if inspectPacket == nil {
		return frame, nil
	}
	out, err := inspectPacket(dir, frame)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("MySQL handshake inspector held a complete packet")
	}
	return out, nil
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

	greetingPacket, err := readMySQLHandshakePacket(upstream)
	if err != nil {
		return nil, nil, fmt.Errorf("read MySQL greeting: %w", err)
	}
	if greetingPacket.seq != 0 {
		return nil, nil, fmt.Errorf("MySQL greeting sequence is %d, want 0", greetingPacket.seq)
	}
	greeting, err := parseMySQLGreeting(greetingPacket.payload)
	if err != nil {
		return nil, nil, err
	}
	if greeting.capabilities&mysqlClientSSL == 0 {
		return nil, nil, errors.New("MySQL upstream does not advertise CLIENT_SSL")
	}
	if len(greeting.scramble) == 0 {
		return nil, nil, errors.New("MySQL greeting contains no authentication scramble")
	}

	clientGreeting := append([]byte(nil), greetingPacket.payload...)
	lower := binary.LittleEndian.Uint16(clientGreeting[greeting.capabilityOffset : greeting.capabilityOffset+2])
	binary.LittleEndian.PutUint16(clientGreeting[greeting.capabilityOffset:greeting.capabilityOffset+2], lower&^uint16(mysqlClientSSL))
	downNext := byte(0)
	upNext := byte(1)
	frame := frameMySQLHandshakePacket(downNext, clientGreeting)
	frame, err = inspectMySQLHandshake(inspectPacket, inspect.FromServer, frame)
	if err != nil {
		return nil, nil, err
	}
	if err := writeAll(client, frame); err != nil {
		return nil, nil, fmt.Errorf("write MySQL greeting: %w", err)
	}
	downNext++

	responsePacket, err := readMySQLHandshakePacket(client)
	if err != nil {
		return nil, nil, fmt.Errorf("read MySQL handshake response: %w", err)
	}
	if responsePacket.seq != downNext {
		return nil, nil, fmt.Errorf("MySQL handshake response sequence is %d, want %d", responsePacket.seq, downNext)
	}
	if len(responsePacket.payload) < mysqlProtocol41HeaderLen {
		return nil, nil, fmt.Errorf("MySQL HandshakeResponse41 is %d bytes, want at least %d", len(responsePacket.payload), mysqlProtocol41HeaderLen)
	}
	if _, err := inspectMySQLHandshake(inspectPacket, inspect.FromClient,
		frameMySQLHandshakePacket(responsePacket.seq, responsePacket.payload)); err != nil {
		return nil, nil, err
	}
	downNext++

	sslRequest := append([]byte(nil), responsePacket.payload[:mysqlProtocol41HeaderLen]...)
	flags := binary.LittleEndian.Uint32(sslRequest[:4]) | mysqlClientSSL
	binary.LittleEndian.PutUint32(sslRequest[:4], flags)
	if err := writeAll(upstream, frameMySQLHandshakePacket(upNext, sslRequest)); err != nil {
		return nil, nil, fmt.Errorf("write MySQL SSLRequest: %w", err)
	}
	upNext++

	tlsConn, err := startTLS(upstream, upstreamAddr, inspect.MySQL, tlsCfg)
	if err != nil {
		return nil, nil, err
	}
	upstream = tlsConn
	if err := writeAll(upstream, frameMySQLHandshakePacket(upNext, responsePacket.payload)); err != nil {
		return nil, nil, fmt.Errorf("write encrypted MySQL handshake response: %w", err)
	}
	upNext++

	plugin := greeting.plugin
	scramble := greeting.scramble
	for range mysqlMaxAuthRounds {
		serverPacket, err := readMySQLHandshakePacket(upstream)
		if err != nil {
			return nil, nil, fmt.Errorf("read MySQL authentication response: %w", err)
		}
		if serverPacket.seq != upNext {
			return nil, nil, fmt.Errorf("MySQL upstream authentication sequence is %d, want %d", serverPacket.seq, upNext)
		}
		upNext++
		if p, s, ok := mysqlAuthSwitch(serverPacket.payload); ok {
			plugin, scramble = p, s
		}

		serverFrame := frameMySQLHandshakePacket(downNext, serverPacket.payload)
		serverFrame, err = inspectMySQLHandshake(inspectPacket, inspect.FromServer, serverFrame)
		if err != nil {
			return nil, nil, err
		}
		if err := writeAll(client, serverFrame); err != nil {
			return nil, nil, fmt.Errorf("write MySQL authentication response: %w", err)
		}
		downNext++

		if len(serverPacket.payload) == 0 {
			return nil, nil, errors.New("MySQL upstream sent an empty authentication packet")
		}
		switch serverPacket.payload[0] {
		case mysqlOKPacket, mysqlERRPacket:
			_ = client.SetDeadline(time.Time{})
			_ = upstream.SetDeadline(time.Time{})
			return client, upstream, nil
		}
		if bytes.Equal(serverPacket.payload, []byte{mysqlAuthMoreData, 0x03}) {
			continue
		}

		clientPacket, err := readMySQLHandshakePacket(client)
		if err != nil {
			return nil, nil, fmt.Errorf("read MySQL authentication response from client: %w", err)
		}
		if clientPacket.seq != downNext {
			return nil, nil, fmt.Errorf("MySQL client authentication sequence is %d, want %d", clientPacket.seq, downNext)
		}
		if _, err := inspectMySQLHandshake(inspectPacket, inspect.FromClient,
			frameMySQLHandshakePacket(clientPacket.seq, clientPacket.payload)); err != nil {
			return nil, nil, err
		}
		downNext++

		if mysqlRequestsPublicKey(plugin, clientPacket.payload) {
			publicKeyPayload := append([]byte{mysqlAuthMoreData}, bridge.publicPEM...)
			publicKeyFrame := frameMySQLHandshakePacket(downNext, publicKeyPayload)
			publicKeyFrame, err = inspectMySQLHandshake(inspectPacket, inspect.FromServer, publicKeyFrame)
			if err != nil {
				return nil, nil, err
			}
			if err := writeAll(client, publicKeyFrame); err != nil {
				return nil, nil, fmt.Errorf("write MySQL authentication public key: %w", err)
			}
			downNext++

			encryptedPacket, err := readMySQLHandshakePacket(client)
			if err != nil {
				return nil, nil, fmt.Errorf("read encrypted MySQL authentication response: %w", err)
			}
			if encryptedPacket.seq != downNext {
				return nil, nil, fmt.Errorf("MySQL encrypted authentication sequence is %d, want %d", encryptedPacket.seq, downNext)
			}
			if _, err := inspectMySQLHandshake(inspectPacket, inspect.FromClient,
				frameMySQLHandshakePacket(encryptedPacket.seq, encryptedPacket.payload)); err != nil {
				return nil, nil, err
			}
			downNext++

			password, err := bridge.decryptPassword(plugin, encryptedPacket.payload, scramble)
			if err != nil {
				return nil, nil, err
			}
			err = writeAll(upstream, frameMySQLHandshakePacket(upNext, password))
			clear(password)
			if err != nil {
				return nil, nil, fmt.Errorf("write MySQL full authentication response: %w", err)
			}
			upNext++
			continue
		}

		if err := writeAll(upstream, frameMySQLHandshakePacket(upNext, clientPacket.payload)); err != nil {
			return nil, nil, fmt.Errorf("write MySQL authentication response upstream: %w", err)
		}
		upNext++
	}

	return nil, nil, fmt.Errorf("MySQL authentication exceeded %d round trips", mysqlMaxAuthRounds)
}
