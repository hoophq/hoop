package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
	mysqlauth "github.com/hoophq/libhoop/v2/codec/mysql/auth"
)

const (
	mysqlTestClientCaps = mysqlauth.ClientProtocol41 | mysqlauth.ClientSecureConnection |
		mysqlauth.ClientPluginAuth | mysqlauth.ClientConnectWithDB
	mysqlTestUpstreamScramble = "upstream-nonce-20by!"
)

// mysqlLoginHarness wires a LoginConn to a fake client and a fake upstream
// on pipes and records what the gate was shown.
type mysqlLoginHarness struct {
	lc           *LoginConn
	inspected    []string
	inspectedPkt [][]byte
	clientDone   <-chan error
	serverDone   <-chan error
}

type mysqlLoginOptions struct {
	key           *rsa.PrivateKey
	upstreamTLS   *tls.Config
	downstreamTLS *tls.Config
	authenticate  func(ctx context.Context, user, secret string) (session.Identity, Credential, error)
	passthrough   []string
}

func newMySQLLoginHarness(t *testing.T, opts mysqlLoginOptions, server, client func(net.Conn) error) *mysqlLoginHarness {
	t.Helper()
	bridge, err := newMySQLAuthBridge(opts.key)
	if err != nil {
		t.Fatal(err)
	}
	upstream, serverDone := runMySQLPeer(t, server)
	clientConn, clientDone := runMySQLPeer(t, client)
	deadline := time.Now().Add(5 * time.Second)
	_ = upstream.SetDeadline(deadline)
	_ = clientConn.SetDeadline(deadline)
	h := &mysqlLoginHarness{clientDone: clientDone, serverDone: serverDone}
	h.lc = &LoginConn{
		Client: clientConn,
		Dial: func(context.Context, *Credential) (net.Conn, error) {
			return upstream, nil
		},
		UpstreamTLS:   opts.upstreamTLS,
		DownstreamTLS: opts.downstreamTLS,
		MySQLAuth:     bridge,
		Lane:          LaneInfo{Protocol: inspect.MySQL, Network: "tcp", UpstreamAddr: "mysql:3306"},
		Authenticate:  opts.authenticate,
		Passthrough: func(user string) bool {
			for _, u := range opts.passthrough {
				if u == user {
					return true
				}
			}
			return false
		},
		Inspect: func(dir inspect.Direction, pkt []byte) error {
			h.inspected = append(h.inspected, fmt.Sprintf("%s:%d", dir, pkt[3]))
			h.inspectedPkt = append(h.inspectedPkt, append([]byte(nil), pkt...))
			return nil
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	t.Cleanup(func() {
		clientConn.Close()
		upstream.Close()
	})
	return h
}

func (h *mysqlLoginHarness) wait(t *testing.T) {
	t.Helper()
	if err := <-h.clientDone; err != nil {
		t.Errorf("client: %v", err)
	}
	if err := <-h.serverDone; err != nil {
		t.Errorf("server: %v", err)
	}
}

func mysqlTestAuthenticate(t *testing.T, wantUser, wantSecret string, cred Credential) func(context.Context, string, string) (session.Identity, Credential, error) {
	return func(_ context.Context, user, secret string) (session.Identity, Credential, error) {
		if user != wantUser || secret != wantSecret {
			return session.Identity{}, Credential{}, fmt.Errorf("%w: %s", ErrLoginRefused, user)
		}
		return session.Identity{Subject: user, Email: user + "@example.com"}, cred, nil
	}
}

// mysqlTestClient is a client that logs in as user with password and
// expects the final packet to be an OK, or to satisfy check when given.
// On a plaintext leg it asks the relay for its public key; on TLS it sends
// the password in the clear, as libmysqlclient does.
func mysqlTestClient(t *testing.T, user, password string, clientTLS bool, check func(mysqlPacket) error) func(net.Conn) error {
	return func(conn net.Conn) error {
		greeting := mustReadMySQLPacket(t, conn)
		h, err := mysqlauth.ParseHandshake(greeting.payload)
		if err != nil {
			return err
		}
		if greeting.seq != 0 || h.Plugin != mysqlCachingSHA2 || len(h.Scramble) != mysqlauth.ScrambleLen ||
			bytes.Equal(h.Scramble, []byte(mysqlTestUpstreamScramble)) || bytes.IndexByte(h.Scramble, 0) >= 0 {
			return fmt.Errorf("relay greeting = seq %d, plugin %q, scramble %x", greeting.seq, h.Plugin, h.Scramble)
		}
		if (h.Capabilities&mysqlClientSSL != 0) != clientTLS {
			return fmt.Errorf("relay greeting CLIENT_SSL = %v, want %v", h.Capabilities&mysqlClientSSL != 0, clientTLS)
		}
		resp := mysqlauth.HandshakeResponse41{
			Capabilities: mysqlTestClientCaps, MaxPacket: 1 << 24, Charset: 45,
			User: user, AuthResponse: mysqlauth.ScrambleSHA256(h.Scramble, password),
			Database: "appdb", Plugin: mysqlCachingSHA2,
		}
		seq := byte(1)
		if clientTLS {
			mustWriteMySQLPacket(t, conn, seq, mysqlauth.EncodeSSLRequest(nil, resp.Capabilities|mysqlClientSSL, resp.MaxPacket, resp.Charset))
			tc := tls.Client(conn, &tls.Config{InsecureSkipVerify: true})
			if err := tc.Handshake(); err != nil {
				return err
			}
			conn = tc
			resp.Capabilities |= mysqlClientSSL
			seq++
		}
		mustWriteMySQLPacket(t, conn, seq, mysqlauth.EncodeHandshakeResponse41(resp))
		seq++

		fullAuth := mustReadMySQLPacket(t, conn)
		if fullAuth.seq != seq || !bytes.Equal(fullAuth.payload, []byte{mysqlAuthMoreData, mysqlauth.CachingSHA2PerformFullAuth}) {
			return fmt.Errorf("full-auth request = seq %d, payload %x", fullAuth.seq, fullAuth.payload)
		}
		seq++
		if clientTLS {
			mustWriteMySQLPacket(t, conn, seq, append([]byte(password), 0))
			seq++
		} else {
			mustWriteMySQLPacket(t, conn, seq, []byte{mysqlauth.CachingSHA2RequestPublicKey})
			seq++
			key := mustReadMySQLPacket(t, conn)
			pem, ok := mysqlauth.ParseAuthMoreData(key.payload)
			if key.seq != seq || !ok {
				return fmt.Errorf("public-key response = seq %d, payload %x", key.seq, key.payload)
			}
			pub, err := mysqlauth.ParsePublicKeyPEM(pem)
			if err != nil {
				return err
			}
			seq++
			ciphertext, err := mysqlauth.EncryptPasswordRSA(password, h.Scramble, pub)
			if err != nil {
				return err
			}
			mustWriteMySQLPacket(t, conn, seq, ciphertext)
			seq++
		}
		result := mustReadMySQLPacket(t, conn)
		if result.seq != seq {
			return fmt.Errorf("authentication result = seq %d, want %d", result.seq, seq)
		}
		if check != nil {
			return check(result)
		}
		if len(result.payload) == 0 || result.payload[0] != mysqlOKPacket {
			return fmt.Errorf("authentication result = %x, want OK", result.payload)
		}
		return nil
	}
}

// mysqlTestUpstream is a server that greets under plugin and checks the
// HandshakeResponse41 names wantUser with wantAuth, then runs rest.
func mysqlTestUpstream(t *testing.T, plugin string, serverTLS *tls.Config, wantUser string, wantAuth []byte,
	rest func(conn net.Conn, seq byte) error) func(net.Conn) error {
	return func(conn net.Conn) error {
		mustWriteMySQLPacket(t, conn, 0, mysqlTestGreeting(plugin, []byte(mysqlTestUpstreamScramble), serverTLS != nil))
		seq := byte(1)
		if serverTLS != nil {
			sslRequest := mustReadMySQLPacket(t, conn)
			if sslRequest.seq != 1 || len(sslRequest.payload) != mysqlProtocol41HeaderLen ||
				binary.LittleEndian.Uint32(sslRequest.payload)&mysqlClientSSL == 0 {
				return fmt.Errorf("SSLRequest = seq %d, %d bytes", sslRequest.seq, len(sslRequest.payload))
			}
			tc := tls.Server(conn, serverTLS)
			if err := tc.Handshake(); err != nil {
				return err
			}
			conn = tc
			seq++
		}
		response := mustReadMySQLPacket(t, conn)
		resp, err := mysqlauth.ParseHandshakeResponse41(response.payload)
		if err != nil {
			return err
		}
		if response.seq != seq || resp.User != wantUser || resp.Database != "appdb" || resp.Plugin != plugin ||
			!bytes.Equal(resp.AuthResponse, wantAuth) {
			return fmt.Errorf("HandshakeResponse41 = seq %d, user %q, db %q, plugin %q, auth %x",
				response.seq, resp.User, resp.Database, resp.Plugin, resp.AuthResponse)
		}
		if resp.Capabilities&mysqlClientSSL != 0 != (serverTLS != nil) {
			return fmt.Errorf("HandshakeResponse41 CLIENT_SSL = %v", resp.Capabilities&mysqlClientSSL != 0)
		}
		return rest(conn, seq+1)
	}
}

func mysqlOK() []byte { return mysqlauth.EncodeOK(mysqlauth.ServerStatusAutocommit) }

func TestMySQLLoginTerminatesCachingSHA2FullAuthOverUpstreamTLS(t *testing.T) {
	cred := Credential{Name: "svc-pool", User: "svc", Password: "svcpw"}
	serverTLS := testTLSConfig(t)
	h := newMySQLLoginHarness(t, mysqlLoginOptions{
		upstreamTLS:  &tls.Config{InsecureSkipVerify: true},
		authenticate: mysqlTestAuthenticate(t, "alice", "alicepw", cred),
	},
		mysqlTestUpstream(t, mysqlCachingSHA2, serverTLS, "svc",
			mysqlauth.ScrambleSHA256([]byte(mysqlTestUpstreamScramble), "svcpw"),
			func(conn net.Conn, seq byte) error {
				mustWriteMySQLPacket(t, conn, seq, []byte{mysqlAuthMoreData, mysqlauth.CachingSHA2PerformFullAuth})
				password := mustReadMySQLPacket(t, conn)
				if password.seq != seq+1 || !bytes.Equal(password.payload, []byte("svcpw\x00")) {
					return fmt.Errorf("full-auth response = seq %d, payload %q", password.seq, password.payload)
				}
				mustWriteMySQLPacket(t, conn, seq+2, mysqlOK())
				return nil
			}),
		mysqlTestClient(t, "alice", "alicepw", false, nil),
	)

	result, err := loginMySQL(context.Background(), h.lc)
	if err != nil {
		t.Fatalf("loginMySQL: %v", err)
	}
	h.wait(t)
	if result.Identity.Subject != "alice" || result.Identity.Email != "alice@example.com" || result.Passthrough {
		t.Fatalf("identity = %+v", result.Identity)
	}
	if result.Credential.Name != "svc-pool" || result.Credential.User != "svc" {
		t.Fatalf("credential = %+v", result.Credential)
	}
	if _, ok := h.lc.Upstream.(*tls.Conn); !ok {
		t.Fatalf("upstream is %T, want *tls.Conn", h.lc.Upstream)
	}
	want := []string{"server:0", "client:1", "server:2", "client:3", "server:4", "client:5", "server:6"}
	if !reflect.DeepEqual(h.inspected, want) {
		t.Fatalf("gate saw %v, want %v", h.inspected, want)
	}
}

func TestMySQLLoginAnswersUpstreamPlugins(t *testing.T) {
	cred := Credential{Name: "svc-pool", User: "svc", Password: "svcpw"}
	scramble := []byte(mysqlTestUpstreamScramble)
	switched := []byte("switched-nonce-20by!")
	cases := []struct {
		name   string
		plugin string
		auth   []byte
		rest   func(conn net.Conn, seq byte) error
	}{
		{
			name:   "caching_sha2 fast auth",
			plugin: mysqlCachingSHA2,
			auth:   mysqlauth.ScrambleSHA256(scramble, "svcpw"),
			rest: func(conn net.Conn, seq byte) error {
				mustWriteMySQLPacket(t, conn, seq, []byte{mysqlAuthMoreData, mysqlauth.CachingSHA2FastAuthSuccess})
				mustWriteMySQLPacket(t, conn, seq+1, mysqlOK())
				return nil
			},
		},
		{
			name:   "caching_sha2 full auth over plaintext asks for the server key",
			plugin: mysqlCachingSHA2,
			auth:   mysqlauth.ScrambleSHA256(scramble, "svcpw"),
			rest: func(conn net.Conn, seq byte) error {
				key, err := rsa.GenerateKey(rand.Reader, 2048)
				if err != nil {
					return err
				}
				mustWriteMySQLPacket(t, conn, seq, []byte{mysqlAuthMoreData, mysqlauth.CachingSHA2PerformFullAuth})
				request := mustReadMySQLPacket(t, conn)
				if request.seq != seq+1 || !bytes.Equal(request.payload, []byte{mysqlauth.CachingSHA2RequestPublicKey}) {
					return fmt.Errorf("public-key request = seq %d, payload %x", request.seq, request.payload)
				}
				pem, err := mysqlauth.EncodePublicKeyPEM(&key.PublicKey)
				if err != nil {
					return err
				}
				mustWriteMySQLPacket(t, conn, seq+2, mysqlauth.EncodeAuthMoreData(pem))
				encrypted := mustReadMySQLPacket(t, conn)
				password, err := mysqlauth.DecryptPasswordRSA(encrypted.payload, scramble, key)
				if encrypted.seq != seq+3 || err != nil || string(password) != "svcpw" {
					return fmt.Errorf("encrypted password = seq %d, %q, %v", encrypted.seq, password, err)
				}
				mustWriteMySQLPacket(t, conn, seq+4, mysqlOK())
				return nil
			},
		},
		{
			name:   "mysql_native_password",
			plugin: mysqlauth.NativePassword,
			auth:   mysqlauth.ScrambleNative(scramble, "svcpw"),
			rest: func(conn net.Conn, seq byte) error {
				mustWriteMySQLPacket(t, conn, seq, mysqlOK())
				return nil
			},
		},
		{
			name:   "auth switch to native with a new nonce",
			plugin: mysqlCachingSHA2,
			auth:   mysqlauth.ScrambleSHA256(scramble, "svcpw"),
			rest: func(conn net.Conn, seq byte) error {
				mustWriteMySQLPacket(t, conn, seq, mysqlauth.EncodeAuthSwitchRequest(mysqlauth.NativePassword, switched))
				proof := mustReadMySQLPacket(t, conn)
				if proof.seq != seq+1 || !bytes.Equal(proof.payload, mysqlauth.ScrambleNative(switched, "svcpw")) {
					return fmt.Errorf("switch response = seq %d, payload %x", proof.seq, proof.payload)
				}
				mustWriteMySQLPacket(t, conn, seq+2, mysqlOK())
				return nil
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newMySQLLoginHarness(t, mysqlLoginOptions{
				authenticate: mysqlTestAuthenticate(t, "alice", "alicepw", cred),
			},
				mysqlTestUpstream(t, tc.plugin, nil, "svc", tc.auth, tc.rest),
				mysqlTestClient(t, "alice", "alicepw", false, nil),
			)
			result, err := loginMySQL(context.Background(), h.lc)
			if err != nil {
				t.Fatalf("loginMySQL: %v", err)
			}
			h.wait(t)
			if result.Identity.Subject != "alice" || result.Credential.User != "svc" {
				t.Fatalf("result = %+v", result)
			}
			if last := h.inspected[len(h.inspected)-1]; last != "server:6" {
				t.Fatalf("gate saw %v, want the OK last", h.inspected)
			}
		})
	}
}

func TestMySQLLoginRefusesWrongSecret(t *testing.T) {
	released := false
	upstreamSaw := make(chan error, 1)
	h := newMySQLLoginHarness(t, mysqlLoginOptions{
		authenticate: mysqlTestAuthenticate(t, "alice", "alicepw", Credential{
			Release: func(context.Context) error { released = true; return nil },
		}),
	},
		func(conn net.Conn) error {
			mustWriteMySQLPacket(t, conn, 0, mysqlTestGreeting(mysqlCachingSHA2, []byte(mysqlTestUpstreamScramble), false))
			// Nothing of a refused client reaches the upstream.
			_, err := readMySQLHandshakeMessage(conn)
			upstreamSaw <- err
			return nil
		},
		mysqlTestClient(t, "alice", "wrong", false, func(result mysqlPacket) error {
			e, ok := mysqlauth.ParseERR(result.payload)
			if !ok || e.Code != mysqlauth.ErrAccessDenied || e.SQLState != mysqlauth.SQLStateAccessDenied ||
				!strings.Contains(e.Message, "Access denied for user 'alice'") {
				return fmt.Errorf("refusal = %x", result.payload)
			}
			return nil
		}),
	)

	result, err := loginMySQL(context.Background(), h.lc)
	if !errors.Is(err, ErrLoginRefused) {
		t.Fatalf("error = %v, want ErrLoginRefused", err)
	}
	if result.Identity.Subject != "alice" {
		t.Fatalf("refused identity = %+v, want the claimed user", result.Identity)
	}
	if err := <-h.clientDone; err != nil {
		t.Fatalf("client: %v", err)
	}
	h.lc.Upstream.Close()
	if err := <-upstreamSaw; err == nil {
		t.Fatal("upstream received a handshake response for a refused login")
	}
	<-h.serverDone
	if released {
		t.Fatal("no credential was selected, none should be released")
	}
	if last := h.inspected[len(h.inspected)-1]; last != "server:6" {
		t.Fatalf("gate saw %v, want the ERR last", h.inspected)
	}
}

func TestMySQLLoginPassesListedUserThrough(t *testing.T) {
	scramble := []byte(mysqlTestUpstreamScramble)
	proof := mysqlauth.ScrambleNative(scramble, "legacypw")
	h := newMySQLLoginHarness(t, mysqlLoginOptions{
		passthrough: []string{"legacy"},
		authenticate: func(context.Context, string, string) (session.Identity, Credential, error) {
			return session.Identity{}, Credential{}, errors.New("not consulted for a pass-through user")
		},
	},
		mysqlTestUpstream(t, mysqlauth.NativePassword, nil, "legacy", proof, func(conn net.Conn, seq byte) error {
			mustWriteMySQLPacket(t, conn, seq, mysqlOK())
			return nil
		}),
		func(conn net.Conn) error {
			greeting := mustReadMySQLPacket(t, conn)
			relay, err := mysqlauth.ParseHandshake(greeting.payload)
			if err != nil {
				return err
			}
			mustWriteMySQLPacket(t, conn, 1, mysqlauth.EncodeHandshakeResponse41(mysqlauth.HandshakeResponse41{
				Capabilities: mysqlTestClientCaps, MaxPacket: 1 << 24, Charset: 45,
				User: "legacy", AuthResponse: mysqlauth.ScrambleSHA256(relay.Scramble, "legacypw"),
				Database: "appdb", Plugin: mysqlCachingSHA2,
			}))
			// The upstream's own plugin and nonce, so the next proof is one
			// the upstream can check.
			switchReq := mustReadMySQLPacket(t, conn)
			plugin, nonce, ok := mysqlauth.ParseAuthSwitchRequest(switchReq.payload)
			if switchReq.seq != 2 || !ok || plugin != mysqlauth.NativePassword || !bytes.Equal(nonce, scramble) {
				return fmt.Errorf("auth switch = seq %d, %q %x", switchReq.seq, plugin, nonce)
			}
			mustWriteMySQLPacket(t, conn, 3, proof)
			result := mustReadMySQLPacket(t, conn)
			if result.seq != 4 || result.payload[0] != mysqlOKPacket {
				return fmt.Errorf("authentication result = seq %d, payload %x", result.seq, result.payload)
			}
			return nil
		},
	)

	result, err := loginMySQL(context.Background(), h.lc)
	if err != nil {
		t.Fatalf("loginMySQL: %v", err)
	}
	h.wait(t)
	if !result.Passthrough || result.Identity.Subject != "legacy" || result.Credential.Name != "" {
		t.Fatalf("result = %+v", result)
	}
	want := []string{"server:0", "client:1", "server:2", "client:3", "server:4"}
	if !reflect.DeepEqual(h.inspected, want) {
		t.Fatalf("gate saw %v, want %v", h.inspected, want)
	}
}

func TestMySQLLoginTerminatesDownstreamTLS(t *testing.T) {
	cred := Credential{Name: "svc-pool", User: "svc", Password: "svcpw"}
	h := newMySQLLoginHarness(t, mysqlLoginOptions{
		downstreamTLS: testTLSConfig(t),
		authenticate:  mysqlTestAuthenticate(t, "alice", "alicepw", cred),
	},
		mysqlTestUpstream(t, mysqlCachingSHA2, nil, "svc",
			mysqlauth.ScrambleSHA256([]byte(mysqlTestUpstreamScramble), "svcpw"),
			func(conn net.Conn, seq byte) error {
				mustWriteMySQLPacket(t, conn, seq, []byte{mysqlAuthMoreData, mysqlauth.CachingSHA2FastAuthSuccess})
				mustWriteMySQLPacket(t, conn, seq+1, mysqlOK())
				return nil
			}),
		mysqlTestClient(t, "alice", "alicepw", true, nil),
	)

	result, err := loginMySQL(context.Background(), h.lc)
	if err != nil {
		t.Fatalf("loginMySQL: %v", err)
	}
	h.wait(t)
	if result.Identity.Subject != "alice" {
		t.Fatalf("result = %+v", result)
	}
	if _, ok := h.lc.Client.(*tls.Conn); !ok {
		t.Fatalf("client is %T, want *tls.Conn", h.lc.Client)
	}
	// The SSLRequest never reaches the gate, and the response it sees
	// does not announce TLS: it reads the decrypted bytes.
	want := []string{"server:0", "client:2", "server:3", "client:4", "server:5"}
	if !reflect.DeepEqual(h.inspected, want) {
		t.Fatalf("gate saw %v, want %v", h.inspected, want)
	}
	if caps := binary.LittleEndian.Uint32(h.inspectedPkt[1][4:]); caps&mysqlClientSSL != 0 {
		t.Fatalf("gate saw CLIENT_SSL in the handshake response: %#x", caps)
	}
}

func TestMySQLLoginReadsPinnedKeyResponse(t *testing.T) {
	cred := Credential{Name: "svc-pool", User: "svc", Password: "svcpw"}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pinnedClient := func(expectOK bool) func(net.Conn) error {
		return func(conn net.Conn) error {
			greeting := mustReadMySQLPacket(t, conn)
			relay, err := mysqlauth.ParseHandshake(greeting.payload)
			if err != nil {
				return err
			}
			mustWriteMySQLPacket(t, conn, 1, mysqlauth.EncodeHandshakeResponse41(mysqlauth.HandshakeResponse41{
				Capabilities: mysqlTestClientCaps, MaxPacket: 1 << 24, Charset: 45,
				User: "alice", AuthResponse: mysqlauth.ScrambleSHA256(relay.Scramble, "alicepw"),
				Database: "appdb", Plugin: mysqlCachingSHA2,
			}))
			if more := mustReadMySQLPacket(t, conn); more.seq != 2 {
				return fmt.Errorf("full-auth request = seq %d", more.seq)
			}
			ciphertext, err := mysqlauth.EncryptPasswordRSA("alicepw", relay.Scramble, &key.PublicKey)
			if err != nil {
				return err
			}
			mustWriteMySQLPacket(t, conn, 3, ciphertext)
			result := mustReadMySQLPacket(t, conn)
			if result.seq != 4 {
				return fmt.Errorf("authentication result = seq %d", result.seq)
			}
			if expectOK {
				if result.payload[0] != mysqlOKPacket {
					return fmt.Errorf("authentication result = %x", result.payload)
				}
				return nil
			}
			if result.payload[0] != mysqlERRPacket || !bytes.Contains(result.payload, []byte("mysql_auth_key_file")) {
				return fmt.Errorf("authentication result = %q", result.payload)
			}
			return nil
		}
	}
	fastUpstream := func() func(net.Conn) error {
		return mysqlTestUpstream(t, mysqlCachingSHA2, nil, "svc",
			mysqlauth.ScrambleSHA256([]byte(mysqlTestUpstreamScramble), "svcpw"),
			func(conn net.Conn, seq byte) error {
				mustWriteMySQLPacket(t, conn, seq, []byte{mysqlAuthMoreData, mysqlauth.CachingSHA2FastAuthSuccess})
				mustWriteMySQLPacket(t, conn, seq+1, mysqlOK())
				return nil
			})
	}

	t.Run("lane key", func(t *testing.T) {
		h := newMySQLLoginHarness(t, mysqlLoginOptions{
			key:          key,
			authenticate: mysqlTestAuthenticate(t, "alice", "alicepw", cred),
		}, fastUpstream(), pinnedClient(true))
		if _, err := loginMySQL(context.Background(), h.lc); err != nil {
			t.Fatalf("loginMySQL: %v", err)
		}
		h.wait(t)
	})
	t.Run("no lane key", func(t *testing.T) {
		h := newMySQLLoginHarness(t, mysqlLoginOptions{
			authenticate: mysqlTestAuthenticate(t, "alice", "alicepw", cred),
		},
			func(conn net.Conn) error {
				mustWriteMySQLPacket(t, conn, 0, mysqlTestGreeting(mysqlCachingSHA2, []byte(mysqlTestUpstreamScramble), false))
				return nil
			},
			pinnedClient(false))
		_, err := loginMySQL(context.Background(), h.lc)
		if err == nil || errors.Is(err, ErrLoginRefused) || !strings.Contains(err.Error(), mysqlDirectRSAErrorMessage) {
			t.Fatalf("error = %v, want the direct RSA configuration error", err)
		}
		h.wait(t)
	})
}

func TestMySQLLoginReportsUpstreamRefusal(t *testing.T) {
	cred := Credential{Name: "svc-pool", User: "svc", Password: "stale"}
	h := newMySQLLoginHarness(t, mysqlLoginOptions{
		authenticate: mysqlTestAuthenticate(t, "alice", "alicepw", cred),
	},
		mysqlTestUpstream(t, mysqlCachingSHA2, nil, "svc",
			mysqlauth.ScrambleSHA256([]byte(mysqlTestUpstreamScramble), "stale"),
			func(conn net.Conn, seq byte) error {
				mustWriteMySQLPacket(t, conn, seq, mysqlauth.EncodeERR(mysqlauth.ErrAccessDenied,
					mysqlauth.SQLStateAccessDenied, "Access denied for user 'svc'@'relay' (using password: YES)"))
				return nil
			}),
		mysqlTestClient(t, "alice", "alicepw", false, func(result mysqlPacket) error {
			e, ok := mysqlauth.ParseERR(result.payload)
			if !ok || e.Code != mysqlauth.ErrAccessDenied || !strings.Contains(e.Message, `credential "svc-pool"`) ||
				strings.Contains(e.Message, "svc'") {
				return fmt.Errorf("client was told %q", result.payload)
			}
			return nil
		}),
	)

	_, err := loginMySQL(context.Background(), h.lc)
	if err == nil || errors.Is(err, ErrLoginRefused) || !strings.Contains(err.Error(), `credential "svc-pool" as "svc"`) {
		t.Fatalf("error = %v, want an upstream refusal naming the credential", err)
	}
	h.wait(t)
}

// TestMySQLLoginThroughServer runs the driver under the relay itself, over
// TCP, and sends a statement after the login to prove the pumps took over
// where the login left the legs.
func TestMySQLLoginThroughServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	upstreamDone := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			upstreamDone <- err
			return
		}
		defer conn.Close()
		upstreamDone <- mysqlTestUpstream(t, mysqlCachingSHA2, nil, "svc",
			mysqlauth.ScrambleSHA256([]byte(mysqlTestUpstreamScramble), "svcpw"),
			func(conn net.Conn, seq byte) error {
				mustWriteMySQLPacket(t, conn, seq, []byte{mysqlAuthMoreData, mysqlauth.CachingSHA2FastAuthSuccess})
				mustWriteMySQLPacket(t, conn, seq+1, mysqlOK())
				query := mustReadMySQLPacket(t, conn)
				if query.seq != 0 || string(query.payload) != "\x03SELECT 1" {
					return fmt.Errorf("after login: seq %d, %q", query.seq, query.payload)
				}
				// A one-column result set: column count, definition, EOF, row, EOF.
				mustWriteMySQLPacket(t, conn, 1, []byte{1})
				mustWriteMySQLPacket(t, conn, 2, []byte{3, 'd', 'e', 'f', 0, 0, 0, 1, '1', 0, 0x0c, 0x3f, 0, 1, 0, 0, 0, 0x08, 0x81, 0, 0, 0, 0})
				mustWriteMySQLPacket(t, conn, 3, []byte{0xfe, 0, 0, 2, 0})
				mustWriteMySQLPacket(t, conn, 4, []byte{1, '1'})
				mustWriteMySQLPacket(t, conn, 5, []byte{0xfe, 0, 0, 2, 0})
				return nil
			})(conn)
	}()

	sink := audit.NewMemorySink(64)
	s, err := NewServer(Config{
		Listen:     "127.0.0.1:0",
		Upstream:   ln.Addr().String(),
		Protocol:   inspect.MySQL,
		Connection: "mysql-lane",
		Audit:      sink,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Authenticate: mysqlTestAuthenticate(t, "alice", "alicepw",
			Credential{Name: "svc-pool", User: "svc", Password: "svcpw"}),
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Serve(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for s.Addr() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Addr() == nil {
		t.Fatal("server did not bind")
	}
	defer s.Close()

	conn, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := mysqlTestClient(t, "alice", "alicepw", false, nil)(conn); err != nil {
		t.Fatalf("client login: %v", err)
	}
	mustWriteMySQLPacket(t, conn, 0, []byte("\x03SELECT 1"))
	for seq := byte(1); seq <= 5; seq++ {
		if pkt := mustReadMySQLPacket(t, conn); pkt.seq != seq {
			t.Fatalf("result packet seq = %d, want %d", pkt.seq, seq)
		}
	}
	conn.Close()
	if err := <-upstreamDone; err != nil {
		t.Fatalf("upstream: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n := sink.Len(); n >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	var kinds []string
	for _, ev := range sink.Events() {
		kinds = append(kinds, string(ev.Kind))
		if ev.Principal != "alice" {
			t.Errorf("%s event names %q, want the verified user", ev.Kind, ev.Principal)
		}
		if ev.Kind == audit.KindSessionStart {
			if ev.Metadata["credential"] != "svc-pool" || ev.Metadata["upstream_user"] != "svc" || ev.Metadata["verified"] != "true" {
				t.Errorf("session_start metadata = %v", ev.Metadata)
			}
		}
	}
	if len(kinds) < 3 || kinds[0] != string(audit.KindSessionStart) {
		t.Fatalf("audit kinds = %v", kinds)
	}
}

// FuzzMySQLLogin drives the two parsers that read the client's login: the
// toolkit's HandshakeResponse41 decoder and the relay's splice locator.
// They must agree on the auth response and plugin they find, or an RSA
// rewrite would replace the wrong bytes.
func FuzzMySQLLogin(f *testing.F) {
	f.Add(mysqlauth.EncodeHandshakeResponse41(mysqlauth.HandshakeResponse41{
		Capabilities: mysqlTestClientCaps, MaxPacket: 1 << 24, Charset: 45,
		User: "alice", AuthResponse: make([]byte, 32), Database: "appdb", Plugin: mysqlCachingSHA2,
	}))
	f.Add(mysqlTestHandshakeResponseWithAuth(mysqlSHA256Password, make([]byte, 256)))
	f.Add(mysqlTestHandshakeResponse(mysqlCachingSHA2))
	f.Add(mysqlauth.EncodeSSLRequest(nil, mysqlTestClientCaps|mysqlClientSSL, 1<<24, 45))
	f.Add(mysqlTestGreeting(mysqlCachingSHA2, []byte(mysqlTestUpstreamScramble), true))
	f.Fuzz(func(t *testing.T, payload []byte) {
		_, _ = mysqlauth.ParseHandshake(payload)
		resp, respErr := mysqlauth.ParseHandshakeResponse41(payload)
		splice, spliceErr := parseMySQLHandshakeAuthResponse(payload, "")
		if respErr != nil || spliceErr != nil {
			return
		}
		if splice.authEnd > len(payload) || splice.suffixStart > len(payload) || splice.authStart > splice.authEnd {
			t.Fatalf("splice offsets %+v exceed %d bytes", splice, len(payload))
		}
		if !bytes.Equal(splice.auth, resp.AuthResponse) || splice.plugin != resp.Plugin {
			t.Fatalf("parsers disagree: splice auth %x plugin %q, toolkit auth %x plugin %q",
				splice.auth, splice.plugin, resp.AuthResponse, resp.Plugin)
		}
	})
}
