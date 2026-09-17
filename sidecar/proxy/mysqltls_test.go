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
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
)

const (
	testMySQLClientProtocol41       uint32 = 1 << 9
	testMySQLClientSecureConnection uint32 = 1 << 15
	testMySQLClientPluginAuth       uint32 = 1 << 19
)

func mysqlTestGreeting(plugin string, scramble []byte, withTLS bool) []byte {
	capabilities := testMySQLClientProtocol41 | testMySQLClientSecureConnection | testMySQLClientPluginAuth
	if withTLS {
		capabilities |= mysqlClientSSL
	}
	payload := []byte{10}
	payload = append(payload, []byte("8.4.0-test")...)
	payload = append(payload, 0)
	payload = append(payload, 1, 0, 0, 0)
	payload = append(payload, scramble[:8]...)
	payload = append(payload, 0)
	payload = binary.LittleEndian.AppendUint16(payload, uint16(capabilities))
	payload = append(payload, 45)
	payload = binary.LittleEndian.AppendUint16(payload, 2)
	payload = binary.LittleEndian.AppendUint16(payload, uint16(capabilities>>16))
	payload = append(payload, byte(len(scramble)+1))
	payload = append(payload, make([]byte, 10)...)
	payload = append(payload, scramble[8:]...)
	payload = append(payload, 0)
	payload = append(payload, []byte(plugin)...)
	payload = append(payload, 0)
	return payload
}

func mysqlTestHandshakeResponse(plugin string) []byte {
	flags := testMySQLClientProtocol41 | testMySQLClientSecureConnection | testMySQLClientPluginAuth
	payload := make([]byte, mysqlProtocol41HeaderLen)
	binary.LittleEndian.PutUint32(payload[:4], flags)
	payload[8] = 45
	payload = append(payload, []byte("app")...)
	payload = append(payload, 0, 0)
	payload = append(payload, []byte(plugin)...)
	payload = append(payload, 0)
	return payload
}

func mustReadMySQLPacket(t *testing.T, conn net.Conn) mysqlPacket {
	t.Helper()
	pkt, err := readMySQLHandshakeMessage(conn)
	if err != nil {
		t.Fatalf("read MySQL packet: %v", err)
	}
	return pkt
}

func mustWriteMySQLPacket(t *testing.T, conn net.Conn, seq byte, payload []byte) {
	t.Helper()
	if err := writeAll(conn, frameMySQLHandshakePacket(seq, payload)); err != nil {
		t.Fatalf("write MySQL packet: %v", err)
	}
}

func runMySQLPeer(t *testing.T, fn func(net.Conn) error) (net.Conn, <-chan error) {
	t.Helper()
	relay, peer := net.Pipe()
	done := make(chan error, 1)
	go func() {
		defer peer.Close()
		done <- fn(peer)
	}()
	return relay, done
}

func TestMySQLMessageFragmentsGreetingResponseAndAuthFlow(t *testing.T) {
	const packetSize = 8
	for _, tc := range []struct {
		name    string
		seq     byte
		payload []byte
		nextSeq byte
	}{
		{name: "greeting", seq: 254, payload: []byte("greeting-payload"), nextSeq: 1},
		{name: "response exact multiple", seq: 1, payload: []byte("12345678abcdefgh"), nextSeq: 4},
		{name: "authentication", seq: 7, payload: []byte("authentication-data"), nextSeq: 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relay, peer := net.Pipe()
			done := make(chan error, 1)
			go func() {
				defer peer.Close()
				_, err := writeMySQLMessage(peer, tc.seq, tc.payload, packetSize, nil, inspect.FromClient)
				done <- err
			}()

			got, err := readMySQLMessage(relay, packetSize, 64)
			relay.Close()
			if err != nil {
				t.Fatalf("readMySQLMessage: %v", err)
			}
			if err := <-done; err != nil {
				t.Fatalf("writeMySQLMessage: %v", err)
			}
			if got.seq != tc.seq || got.nextSeq != tc.nextSeq {
				t.Fatalf("sequence = %d..%d, want %d..%d", got.seq, got.nextSeq, tc.seq, tc.nextSeq)
			}
			if !bytes.Equal(got.payload, tc.payload) {
				t.Fatalf("payload = %q, want %q", got.payload, tc.payload)
			}
		})
	}
}

func TestReadMySQLMessageRejectsBrokenContinuationSequence(t *testing.T) {
	relay, peer := net.Pipe()
	done := make(chan error, 1)
	go func() {
		defer peer.Close()
		if err := writeAll(peer, frameMySQLHandshakePacket(4, []byte("12345678"))); err != nil {
			done <- err
			return
		}
		done <- writeAll(peer, frameMySQLHandshakePacket(6, []byte("x")))
	}()

	_, err := readMySQLMessage(relay, 8, 32)
	relay.Close()
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("sequence is 6, want 5")) {
		t.Fatalf("error = %v, want continuation sequence refusal", err)
	}
	<-done
}

func TestReadMySQLMessageRejectsOversizeBeforeBody(t *testing.T) {
	relay, peer := net.Pipe()
	done := make(chan error, 1)
	go func() {
		defer peer.Close()
		done <- writeAll(peer, []byte{5, 0, 0, 1})
	}()

	_, err := readMySQLMessage(relay, 8, 4)
	relay.Close()
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("exceeds 4 bytes")) {
		t.Fatalf("error = %v, want size refusal", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("write header: %v", err)
	}
}

func TestMySQLTLSHandshakeConcurrencyIsBounded(t *testing.T) {
	s := &Server{mysqlHandshakeSlots: make(chan struct{}, mysqlMaxConcurrentHandshakes)}
	for range mysqlMaxConcurrentHandshakes {
		if !s.reserveMySQLHandshake() {
			t.Fatal("handshake slot refused below the limit")
		}
	}
	if s.reserveMySQLHandshake() {
		t.Fatal("handshake slot accepted above the limit")
	}
	s.releaseMySQLHandshake()
	if !s.reserveMySQLHandshake() {
		t.Fatal("released handshake slot was not reusable")
	}
}

func TestNegotiateMySQLUpstreamTLSBridgesCachingSHA2FullAuth(t *testing.T) {
	scramble := []byte("0123456789abcdefghij")
	serverTLS := testTLSConfig(t)

	upstream, serverDone := runMySQLPeer(t, func(conn net.Conn) error {
		if err := writeAll(conn, frameMySQLHandshakePacket(0,
			mysqlTestGreeting(mysqlCachingSHA2, scramble, true))); err != nil {
			return err
		}
		sslRequest, err := readMySQLHandshakeMessage(conn)
		if err != nil {
			return err
		}
		if sslRequest.seq != 1 || len(sslRequest.payload) != mysqlProtocol41HeaderLen {
			return fmt.Errorf("SSLRequest = seq %d, %d bytes", sslRequest.seq, len(sslRequest.payload))
		}
		if binary.LittleEndian.Uint32(sslRequest.payload[:4])&mysqlClientSSL == 0 {
			return fmt.Errorf("SSLRequest does not set CLIENT_SSL")
		}

		tlsConn := tls.Server(conn, serverTLS)
		if err := tlsConn.Handshake(); err != nil {
			return err
		}
		response, err := readMySQLHandshakeMessage(tlsConn)
		if err != nil {
			return err
		}
		if response.seq != 2 || len(response.payload) <= mysqlProtocol41HeaderLen {
			return fmt.Errorf("HandshakeResponse41 = seq %d, %d bytes", response.seq, len(response.payload))
		}
		if err := writeAll(tlsConn, frameMySQLHandshakePacket(3, []byte{mysqlAuthMoreData, 0x04})); err != nil {
			return err
		}
		password, err := readMySQLHandshakeMessage(tlsConn)
		if err != nil {
			return err
		}
		if password.seq != 4 || !bytes.Equal(password.payload, []byte("secret\x00")) {
			return fmt.Errorf("full-auth response = seq %d, payload %q", password.seq, password.payload)
		}
		return writeAll(tlsConn, frameMySQLHandshakePacket(5, []byte{mysqlOKPacket, 0, 0, 2, 0, 0, 0}))
	})

	client, clientDone := runMySQLPeer(t, func(conn net.Conn) error {
		greeting := mustReadMySQLPacket(t, conn)
		parsed, err := parseMySQLGreeting(greeting.payload)
		if err != nil {
			return err
		}
		if greeting.seq != 0 || parsed.capabilities&mysqlClientSSL != 0 {
			return fmt.Errorf("downstream greeting = seq %d, capabilities %#x", greeting.seq, parsed.capabilities)
		}
		mustWriteMySQLPacket(t, conn, 1, mysqlTestHandshakeResponse(mysqlCachingSHA2))

		fullAuth := mustReadMySQLPacket(t, conn)
		if fullAuth.seq != 2 || !bytes.Equal(fullAuth.payload, []byte{mysqlAuthMoreData, 0x04}) {
			return fmt.Errorf("full-auth request = seq %d, payload %x", fullAuth.seq, fullAuth.payload)
		}
		mustWriteMySQLPacket(t, conn, 3, []byte{0x02})

		publicKey := mustReadMySQLPacket(t, conn)
		if publicKey.seq != 4 || len(publicKey.payload) < 2 || publicKey.payload[0] != mysqlAuthMoreData {
			return fmt.Errorf("public-key response = seq %d, payload %x", publicKey.seq, publicKey.payload)
		}
		block, _ := pem.Decode(publicKey.payload[1:])
		if block == nil {
			return fmt.Errorf("public-key response is not PEM")
		}
		parsedKey, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return err
		}
		key, ok := parsedKey.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("public key is %T, want RSA", parsedKey)
		}
		plain := []byte("secret\x00")
		for i := range plain {
			plain[i] ^= scramble[i%len(scramble)]
		}
		ciphertext, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, key, plain, nil)
		clear(plain)
		if err != nil {
			return err
		}
		mustWriteMySQLPacket(t, conn, 5, ciphertext)

		okPacket := mustReadMySQLPacket(t, conn)
		if okPacket.seq != 6 || len(okPacket.payload) == 0 || okPacket.payload[0] != mysqlOKPacket {
			return fmt.Errorf("authentication result = seq %d, payload %x", okPacket.seq, okPacket.payload)
		}
		return nil
	})

	bridge, err := newMySQLAuthBridge()
	if err != nil {
		t.Fatal(err)
	}
	var inspected []string
	inspector := func(dir inspect.Direction, frame []byte) ([]byte, error) {
		inspected = append(inspected, fmt.Sprintf("%s:%d", dir, frame[3]))
		return frame, nil
	}
	gotClient, gotUpstream, err := negotiateMySQLUpstreamTLS(
		client,
		upstream,
		"mysql:3306",
		&tls.Config{InsecureSkipVerify: true}, // test certificate is intentionally private
		5*time.Second,
		bridge,
		inspector,
	)
	if err != nil {
		t.Fatalf("negotiateMySQLUpstreamTLS: %v", err)
	}
	defer gotClient.Close()
	defer gotUpstream.Close()
	if _, ok := gotUpstream.(*tls.Conn); !ok {
		t.Fatalf("upstream connection is %T, want *tls.Conn", gotUpstream)
	}

	if err := <-clientDone; err != nil {
		t.Fatalf("client: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}
	wantInspected := []string{
		"server:0", "client:1", "server:2", "client:3", "server:4", "client:5", "server:6",
	}
	if !reflect.DeepEqual(inspected, wantInspected) {
		t.Fatalf("inspected packets = %v, want %v", inspected, wantInspected)
	}
}

func TestNegotiateMySQLUpstreamTLSPassesOtherAuthenticationPlugins(t *testing.T) {
	scramble := []byte("0123456789abcdefghij")
	serverTLS := testTLSConfig(t)
	nativeReply := []byte("native-password-proof")

	upstream, serverDone := runMySQLPeer(t, func(conn net.Conn) error {
		mustWriteMySQLPacket(t, conn, 0, mysqlTestGreeting(mysqlCachingSHA2, scramble, true))
		if pkt := mustReadMySQLPacket(t, conn); pkt.seq != 1 {
			return fmt.Errorf("SSLRequest sequence = %d", pkt.seq)
		}
		tlsConn := tls.Server(conn, serverTLS)
		if err := tlsConn.Handshake(); err != nil {
			return err
		}
		if pkt := mustReadMySQLPacket(t, tlsConn); pkt.seq != 2 {
			return fmt.Errorf("handshake response sequence = %d", pkt.seq)
		}
		switchPayload := append([]byte{mysqlAuthSwitchRequest}, []byte("mysql_native_password")...)
		switchPayload = append(switchPayload, 0)
		switchPayload = append(switchPayload, scramble...)
		switchPayload = append(switchPayload, 0)
		mustWriteMySQLPacket(t, tlsConn, 3, switchPayload)
		response := mustReadMySQLPacket(t, tlsConn)
		if response.seq != 4 || !bytes.Equal(response.payload, nativeReply) {
			return fmt.Errorf("native response = seq %d, payload %q", response.seq, response.payload)
		}
		mustWriteMySQLPacket(t, tlsConn, 5, []byte{mysqlOKPacket, 0, 0, 2, 0, 0, 0})
		return nil
	})

	client, clientDone := runMySQLPeer(t, func(conn net.Conn) error {
		if pkt := mustReadMySQLPacket(t, conn); pkt.seq != 0 {
			return fmt.Errorf("greeting sequence = %d", pkt.seq)
		}
		mustWriteMySQLPacket(t, conn, 1, mysqlTestHandshakeResponse(mysqlCachingSHA2))
		switchPacket := mustReadMySQLPacket(t, conn)
		if switchPacket.seq != 2 {
			return fmt.Errorf("auth switch sequence = %d", switchPacket.seq)
		}
		mustWriteMySQLPacket(t, conn, 3, nativeReply)
		if pkt := mustReadMySQLPacket(t, conn); pkt.seq != 4 || pkt.payload[0] != mysqlOKPacket {
			return fmt.Errorf("authentication result = seq %d, payload %x", pkt.seq, pkt.payload)
		}
		return nil
	})

	bridge, err := newMySQLAuthBridge()
	if err != nil {
		t.Fatal(err)
	}
	gotClient, gotUpstream, err := negotiateMySQLUpstreamTLS(
		client, upstream, "mysql:3306", &tls.Config{InsecureSkipVerify: true},
		5*time.Second, bridge, nil,
	)
	if err != nil {
		t.Fatalf("negotiateMySQLUpstreamTLS: %v", err)
	}
	defer gotClient.Close()
	defer gotUpstream.Close()
	if err := <-clientDone; err != nil {
		t.Fatalf("client: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}
}

func TestMySQLAuthBridgeSupportsBuiltInRSAPlugins(t *testing.T) {
	scramble := []byte("0123456789abcdefghij")
	bridge, err := newMySQLAuthBridge()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		plugin  string
		request byte
	}{
		{mysqlCachingSHA2, 0x02},
		{mysqlSHA256Password, 0x01},
	} {
		t.Run(tc.plugin, func(t *testing.T) {
			if !mysqlRequestsPublicKey(tc.plugin, []byte{tc.request}) {
				t.Fatal("the plugin's public-key request was not recognized")
			}
			plain := []byte("secret\x00")
			for i := range plain {
				plain[i] ^= scramble[i%len(scramble)]
			}
			ciphertext, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, &bridge.key.PublicKey, plain, nil)
			clear(plain)
			if err != nil {
				t.Fatal(err)
			}
			password, err := bridge.decryptPassword(tc.plugin, ciphertext, scramble)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(password)
			if !bytes.Equal(password, []byte("secret\x00")) {
				t.Fatalf("password = %q, want NUL-terminated secret", password)
			}
		})
	}
}

func TestNegotiateMySQLUpstreamTLSRefusesServerWithoutTLS(t *testing.T) {
	scramble := []byte("0123456789abcdefghij")
	upstream, serverDone := runMySQLPeer(t, func(conn net.Conn) error {
		return writeAll(conn, frameMySQLHandshakePacket(0,
			mysqlTestGreeting(mysqlCachingSHA2, scramble, false)))
	})
	clientRelay, clientPeer := net.Pipe()
	defer clientPeer.Close()
	bridge, err := newMySQLAuthBridge()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = negotiateMySQLUpstreamTLS(
		clientRelay, upstream, "mysql:3306", &tls.Config{InsecureSkipVerify: true},
		5*time.Second, bridge, nil,
	)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("does not advertise CLIENT_SSL")) {
		t.Fatalf("error = %v, want CLIENT_SSL refusal", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}
}
