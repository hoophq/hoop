package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
	mysqlauth "github.com/hoophq/libhoop/v2/codec/mysql/auth"
)

func init() {
	RegisterLogin(inspect.MySQL, loginMySQL)
}

// mysqlLogin is one MySQL login the relay terminates.
//
// MySQL is server-first: the upstream greets, so the relay dials before it
// knows who the client is. The client is greeted by the relay, under
// caching_sha2_password and a nonce of the relay's own, and asked for full
// authentication: the one exchange of that plugin that yields the password
// itself (in the clear under TLS or on a unix socket, RSA-encrypted to the
// relay's key otherwise). A fast-auth proof is a hash the relay cannot
// verify, since it holds no password hash. With the password verified and
// a credential selected, the relay logs in upstream as an ordinary client,
// under the upstream's own plugin and nonce. TLS to the upstream starts
// inside the handshake, after the greeting, so a certificate credential is
// offered there, per identity.
//
// The gate sees the client leg only: the relay's greeting, the client's
// response, the plugin packets between them and the OK or ERR that ends the
// phase. The upstream leg is the relay's own login.
type mysqlLogin struct {
	c             *LoginConn
	inspectPacket mysqlHandshakeInspector
	log           *slog.Logger

	greeting    mysqlauth.Handshake // the upstream's
	greetingSeq byte                // sequence after the upstream greeting
	nonce       []byte
	response    mysqlauth.HandshakeResponse41 // the client's
	downNext    byte
	upstreamTLS bool
}

func loginMySQL(ctx context.Context, c *LoginConn) (LoginResult, error) {
	if c.MySQLAuth == nil {
		return LoginResult{}, errors.New("mysql login needs the lane's authentication key")
	}
	m := &mysqlLogin{
		c:           c,
		log:         c.Log,
		upstreamTLS: c.UpstreamTLS != nil,
		inspectPacket: func(dir inspect.Direction, pkt []byte) ([]byte, error) {
			if err := c.Inspect(dir, pkt); err != nil {
				return nil, err
			}
			return pkt, nil
		},
	}

	upstream, err := c.Dial(ctx, nil)
	if err != nil {
		return LoginResult{}, err
	}
	c.Upstream = upstream
	if err := m.readGreeting(); err != nil {
		return LoginResult{}, err
	}
	if err := m.greetClient(); err != nil {
		return LoginResult{}, err
	}
	if err := m.readResponse(); err != nil {
		return LoginResult{}, err
	}
	user := m.response.User
	if c.Passthrough(user) {
		if err := m.passthrough(); err != nil {
			return LoginResult{}, err
		}
		return LoginResult{Identity: session.Identity{Subject: user}, Passthrough: true}, nil
	}

	password, err := m.clientPassword()
	if err != nil {
		return LoginResult{}, err
	}
	identity, cred, err := c.Authenticate(ctx, user, string(password))
	clear(password)
	if err != nil {
		m.refuse(user)
		if errors.Is(err, ErrLoginRefused) {
			return LoginResult{Identity: session.Identity{Subject: user}}, err
		}
		return LoginResult{}, err
	}
	if err := m.upstreamLogin(cred); err != nil {
		return LoginResult{Identity: identity, Credential: cred}, err
	}
	return LoginResult{Identity: identity, Credential: cred}, nil
}

// readGreeting takes the upstream's greeting. Nothing of it reaches the
// client as bytes; its fields shape the relay's own greeting.
func (m *mysqlLogin) readGreeting() error {
	msg, err := readMySQLHandshakeMessageSeq(m.c.Upstream, 0, "greeting")
	if err != nil {
		return err
	}
	m.greeting, err = mysqlauth.ParseHandshake(msg.payload)
	if err != nil {
		return err
	}
	m.greetingSeq = msg.nextSeq
	if m.upstreamTLS && m.greeting.Capabilities&mysqlClientSSL == 0 {
		return errors.New("MySQL upstream does not advertise CLIENT_SSL")
	}
	if len(m.greeting.Scramble) == 0 {
		return errors.New("MySQL greeting contains no authentication scramble")
	}
	return nil
}

// greetClient sends the relay's greeting: the upstream's version and
// capabilities, so the client negotiates what the upstream supports, under
// caching_sha2_password and the relay's nonce. CLIENT_SSL is offered only
// when the lane terminates downstream TLS.
func (m *mysqlLogin) greetClient() error {
	nonce := make([]byte, mysqlauth.ScrambleLen)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	// mysqld's scramble never holds a NUL; a client may read it as a C
	// string.
	for i := range nonce {
		for nonce[i] == 0 {
			if _, err := rand.Read(nonce[i : i+1]); err != nil {
				return err
			}
		}
	}
	m.nonce = nonce

	g := m.greeting
	g.Plugin = mysqlCachingSHA2
	g.Scramble = nonce
	g.Capabilities |= mysqlClientPluginAuth
	if m.c.DownstreamTLS != nil {
		g.Capabilities |= mysqlClientSSL
	} else {
		g.Capabilities &^= mysqlClientSSL
	}
	var err error
	m.downNext, err = writeMySQLHandshakeMessage(
		m.c.Client, 0, mysqlauth.EncodeHandshake(g), m.inspectPacket, inspect.FromServer)
	if err != nil {
		return fmt.Errorf("write MySQL greeting: %w", err)
	}
	return nil
}

// readResponse takes the client's HandshakeResponse41, terminating TLS
// first when the client asks for it. The gate is shown the response without
// CLIENT_SSL: it reads the decrypted bytes, and the codec refuses a
// handshake that announces ciphertext.
func (m *mysqlLogin) readResponse() error {
	msg, err := readMySQLHandshakeMessageSeq(m.c.Client, m.downNext, "handshake response")
	if err != nil {
		return err
	}
	resp, err := mysqlauth.ParseHandshakeResponse41(msg.payload)
	if err != nil {
		return err
	}
	if resp.Capabilities&mysqlClientSSL != 0 {
		if m.c.DownstreamTLS == nil {
			return errors.New("MySQL client asked for TLS the greeting did not offer")
		}
		if len(msg.payload) != mysqlProtocol41HeaderLen {
			return fmt.Errorf("MySQL SSLRequest is %d bytes, want %d", len(msg.payload), mysqlProtocol41HeaderLen)
		}
		tc := tls.Server(m.c.Client, m.c.DownstreamTLS)
		if err := tc.Handshake(); err != nil {
			return fmt.Errorf("downstream TLS handshake: %w", err)
		}
		m.c.Client = tc
		if msg, err = readMySQLHandshakeMessageSeq(tc, msg.nextSeq, "handshake response"); err != nil {
			return err
		}
		if resp, err = mysqlauth.ParseHandshakeResponse41(msg.payload); err != nil {
			return err
		}
		resp.Capabilities &^= mysqlClientSSL
		payload := append([]byte(nil), msg.payload...)
		binary.LittleEndian.PutUint32(payload[:4], resp.Capabilities)
		msg.payload = payload
	}
	if err := inspectMySQLClientMessage(msg, m.inspectPacket); err != nil {
		return err
	}
	m.response = resp
	m.downNext = msg.nextSeq
	return nil
}

// clientPassword runs caching_sha2_password full authentication against
// the client and returns the password it sent. A client that answered under
// another plugin is switched first; its proof under either is not used.
func (m *mysqlLogin) clientPassword() ([]byte, error) {
	client := m.c.Client
	var err error
	if m.response.Plugin != mysqlCachingSHA2 {
		m.downNext, err = writeMySQLHandshakeMessage(client, m.downNext,
			mysqlauth.EncodeAuthSwitchRequest(mysqlCachingSHA2, m.nonce), m.inspectPacket, inspect.FromServer)
		if err != nil {
			return nil, fmt.Errorf("write MySQL auth switch: %w", err)
		}
		reply, err := readMySQLHandshakeMessageSeq(client, m.downNext, "auth switch response")
		if err != nil {
			return nil, err
		}
		if err := inspectMySQLClientMessage(reply, m.inspectPacket); err != nil {
			return nil, err
		}
		m.downNext = reply.nextSeq
	}

	m.downNext, err = writeMySQLHandshakeMessage(client, m.downNext,
		mysqlauth.EncodeAuthMoreData([]byte{mysqlauth.CachingSHA2PerformFullAuth}), m.inspectPacket, inspect.FromServer)
	if err != nil {
		return nil, fmt.Errorf("write MySQL full authentication request: %w", err)
	}
	reply, err := readMySQLHandshakeMessageSeq(client, m.downNext, "full authentication response")
	if err != nil {
		return nil, err
	}
	if err := inspectMySQLClientMessage(reply, m.inspectPacket); err != nil {
		return nil, err
	}
	m.downNext = reply.nextSeq

	bridge := m.c.MySQLAuth
	payload := reply.payload
	switch {
	case bytes.Equal(payload, []byte{mysqlauth.CachingSHA2RequestPublicKey}):
		m.downNext, err = writeMySQLHandshakeMessage(client, m.downNext,
			mysqlauth.EncodeAuthMoreData(bridge.publicPEM), m.inspectPacket, inspect.FromServer)
		if err != nil {
			return nil, fmt.Errorf("write MySQL authentication public key: %w", err)
		}
		encrypted, err := readMySQLHandshakeMessageSeq(client, m.downNext, "encrypted password")
		if err != nil {
			return nil, err
		}
		if err := inspectMySQLClientMessage(encrypted, m.inspectPacket); err != nil {
			return nil, err
		}
		m.downNext = encrypted.nextSeq
		password, err := mysqlauth.DecryptPasswordRSA(encrypted.payload, m.nonce, bridge.key)
		if err != nil {
			return nil, writeMySQLAuthError(client, m.downNext, "password not readable with the relay's key", m.inspectPacket)
		}
		return password, nil

	case mysqlIsDirectRSAResponse(mysqlCachingSHA2,
		[]byte{mysqlAuthMoreData, mysqlauth.CachingSHA2PerformFullAuth}, payload, bridge):
		// Encrypted to a key the client pinned: readable only when that
		// key is the lane's own.
		if !bridge.configured {
			return nil, writeMySQLAuthError(client, m.downNext, mysqlDirectRSAErrorMessage, m.inspectPacket)
		}
		password, err := mysqlauth.DecryptPasswordRSA(payload, m.nonce, bridge.key)
		if err != nil {
			return nil, writeMySQLAuthError(client, m.downNext, mysqlDirectRSAErrorMessage, m.inspectPacket)
		}
		return password, nil

	case len(payload) > 0 && payload[len(payload)-1] == 0:
		// The clear, NUL-terminated: what a client sends over a transport
		// it holds secure. Taken only where the relay holds it secure too;
		// on plain tcp the password would cross the wire readable.
		if !m.secureClientLeg() {
			return nil, writeMySQLAuthError(client, m.downNext, "a cleartext password is not accepted on a plain connection; use the RSA public key exchange or TLS", m.inspectPacket)
		}
		return append([]byte(nil), payload[:len(payload)-1]...), nil
	}
	return nil, writeMySQLAuthError(client, m.downNext, "full authentication response is not a password", m.inspectPacket)
}

// secureClientLeg reports a client leg a third party cannot read: a unix
// socket, TLS the relay terminated before the login, or TLS the client
// asked for in the handshake.
func (m *mysqlLogin) secureClientLeg() bool {
	return m.c.Lane.Network == "unix" || m.c.Lane.ClientTLS || isTLS(m.c.Client)
}

// refuse tells the client what mysqld tells a wrong password.
func (m *mysqlLogin) refuse(user string) {
	message := fmt.Sprintf("Access denied for user '%s' (using password: YES)", user)
	payload := mysqlauth.EncodeERR(mysqlauth.ErrAccessDenied, mysqlauth.SQLStateAccessDenied, message)
	if _, err := writeMySQLHandshakeMessage(m.c.Client, m.downNext, payload, m.inspectPacket, inspect.FromServer); err != nil {
		m.log.Debug("login refusal not delivered", "error", err)
	}
}

// startUpstreamTLS sends the SSLRequest and upgrades the upstream leg,
// offering cert when one is given. Returns the next upstream sequence id.
func (m *mysqlLogin) startUpstreamTLS(resp mysqlauth.HandshakeResponse41, cert *tls.Certificate) (byte, error) {
	sslRequest := mysqlauth.EncodeSSLRequest(nil, resp.Capabilities|mysqlClientSSL, resp.MaxPacket, resp.Charset)
	upNext, err := writeMySQLHandshakeMessage(m.c.Upstream, m.greetingSeq, sslRequest, nil, inspect.FromClient)
	if err != nil {
		return 0, fmt.Errorf("write MySQL SSLRequest: %w", err)
	}
	tc, err := startTLS(m.c.Upstream, m.c.Lane.UpstreamAddr, inspect.MySQL, tlsClientConfig(m.c.UpstreamTLS, cert))
	if err != nil {
		return 0, err
	}
	m.c.Upstream = tc
	return upNext, nil
}

// passthrough relays the login of a user the lane lets through unchanged.
// The client proved itself against the relay's nonce under a plugin the
// upstream may not run; an auth switch to the upstream's plugin and nonce
// makes its next proof one the upstream can check, and from there the
// exchange is bridged as on a lane without Authenticate.
func (m *mysqlLogin) passthrough() error {
	client := m.c.Client
	var err error
	m.downNext, err = writeMySQLHandshakeMessage(client, m.downNext,
		mysqlauth.EncodeAuthSwitchRequest(m.greeting.Plugin, m.greeting.Scramble), m.inspectPacket, inspect.FromServer)
	if err != nil {
		return fmt.Errorf("write MySQL auth switch: %w", err)
	}
	proof, err := readMySQLHandshakeMessageSeq(client, m.downNext, "auth switch response")
	if err != nil {
		return err
	}
	if err := inspectMySQLClientMessage(proof, m.inspectPacket); err != nil {
		return err
	}
	m.downNext = proof.nextSeq

	resp := m.response
	resp.Capabilities &^= mysqlClientSSL
	resp.Plugin = m.greeting.Plugin
	resp.AuthResponse = proof.payload
	upNext := m.greetingSeq
	if m.upstreamTLS {
		if upNext, err = m.startUpstreamTLS(resp, nil); err != nil {
			return err
		}
		resp.Capabilities |= mysqlClientSSL
	}
	upNext, err = writeMySQLHandshakeMessage(m.c.Upstream, upNext, mysqlauth.EncodeHandshakeResponse41(resp), nil, inspect.FromClient)
	if err != nil {
		return fmt.Errorf("write MySQL handshake response upstream: %w", err)
	}
	relay := &mysqlAuthRelay{
		client:         client,
		upstream:       m.c.Upstream,
		bridge:         m.c.MySQLAuth,
		inspectPacket:  m.inspectPacket,
		plugin:         m.greeting.Plugin,
		scramble:       m.greeting.Scramble,
		downNext:       m.downNext,
		upNext:         upNext,
		upstreamSecure: m.upstreamTLS,
	}
	return relay.run()
}

// upstreamLogin logs in upstream as cred.User, keeping the client's
// negotiated capabilities, database and attributes, and forwards the OK or
// ERR that ends it to the client.
func (m *mysqlLogin) upstreamLogin(cred Credential) error {
	if cred.GSS != nil {
		return errors.New("MySQL login has no GSSAPI path; the credential offers one")
	}
	if cred.Cert != nil && !m.upstreamTLS {
		return errors.New("a certificate credential needs upstream TLS on a MySQL lane")
	}
	resp := m.response
	resp.Capabilities &^= mysqlClientSSL
	resp.User = cred.User
	resp.Plugin = m.greeting.Plugin
	plugin, scramble := m.greeting.Plugin, m.greeting.Scramble
	var err error
	if resp.AuthResponse, err = m.proof(plugin, scramble, cred.Password); err != nil {
		return err
	}
	upNext := m.greetingSeq
	if m.upstreamTLS {
		if upNext, err = m.startUpstreamTLS(resp, cred.Cert); err != nil {
			return err
		}
		resp.Capabilities |= mysqlClientSSL
	}
	upstream := m.c.Upstream
	upNext, err = writeMySQLHandshakeMessage(upstream, upNext, mysqlauth.EncodeHandshakeResponse41(resp), nil, inspect.FromClient)
	if err != nil {
		return fmt.Errorf("write MySQL handshake response upstream: %w", err)
	}

	for range mysqlMaxAuthRounds {
		msg, err := readMySQLHandshakeMessageSeq(upstream, upNext, "upstream authentication")
		if err != nil {
			return err
		}
		upNext = msg.nextSeq
		if len(msg.payload) == 0 {
			return errors.New("MySQL upstream sent an empty authentication packet")
		}
		var reply []byte
		switch msg.payload[0] {
		case mysqlOKPacket:
			_, err := writeMySQLHandshakeMessage(m.c.Client, m.downNext, msg.payload, m.inspectPacket, inspect.FromServer)
			if err != nil {
				return fmt.Errorf("write MySQL authentication result: %w", err)
			}
			return nil
		case mysqlERRPacket:
			// The credential, not the client, was refused. The client
			// learns which pool entry failed, never the upstream user it
			// maps to; the operator finds the upstream's reason in the
			// log.
			e, _ := mysqlauth.ParseERR(msg.payload)
			code, state := mysqlauth.ErrAccessDenied, mysqlauth.SQLStateAccessDenied
			if e != nil && e.SQLState != "" {
				code, state = e.Code, e.SQLState
			}
			refusal := mysqlauth.EncodeERR(code, state, fmt.Sprintf("credential %q refused by upstream", cred.Name))
			_, _ = writeMySQLHandshakeMessage(m.c.Client, m.downNext, refusal, m.inspectPacket, inspect.FromServer)
			return fmt.Errorf("MySQL upstream refused credential %q as %q: %v", cred.Name, cred.User, e)
		case mysqlAuthSwitchRequest:
			p, s, ok := mysqlauth.ParseAuthSwitchRequest(msg.payload)
			if !ok {
				return errors.New("MySQL upstream requested an authentication plugin it did not name")
			}
			plugin = p
			if len(s) > 0 {
				scramble = s
			}
			if reply, err = m.proof(plugin, scramble, cred.Password); err != nil {
				return err
			}
		case mysqlAuthMoreData:
			data := msg.payload[1:]
			switch {
			case bytes.Equal(data, []byte{mysqlauth.CachingSHA2FastAuthSuccess}):
				continue
			case bytes.Equal(data, []byte{mysqlauth.CachingSHA2PerformFullAuth}):
				if m.upstreamTLS {
					reply = append([]byte(cred.Password), 0)
				} else {
					reply = []byte{mysqlauth.CachingSHA2RequestPublicKey}
				}
			case bytes.HasPrefix(data, []byte("-----BEGIN")):
				pub, err := mysqlauth.ParsePublicKeyPEM(data)
				if err != nil {
					return err
				}
				if reply, err = mysqlauth.EncryptPasswordRSA(cred.Password, scramble, pub); err != nil {
					return err
				}
			default:
				return fmt.Errorf("MySQL upstream sent authentication data %x the relay does not answer", data)
			}
		default:
			return fmt.Errorf("MySQL upstream sent an unexpected authentication packet %#x", msg.payload[0])
		}
		if upNext, err = writeMySQLHandshakeMessage(upstream, upNext, reply, nil, inspect.FromClient); err != nil {
			return fmt.Errorf("write MySQL authentication response upstream: %w", err)
		}
	}
	return fmt.Errorf("MySQL authentication exceeded %d round trips", mysqlMaxAuthRounds)
}

// proof is the first authentication response for plugin. sha256_password
// has no scramble form: the password travels in the clear under TLS, and
// over plaintext the reply asks for the server's key, answered when the
// key arrives.
func (m *mysqlLogin) proof(plugin string, scramble []byte, password string) ([]byte, error) {
	if plugin == mysqlSHA256Password {
		if m.upstreamTLS {
			return append([]byte(password), 0), nil
		}
		return []byte{mysqlauth.SHA256RequestPublicKey}, nil
	}
	return mysqlauth.AuthResponse(plugin, scramble, password)
}
