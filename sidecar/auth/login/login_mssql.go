package login

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"

	mssqlauth "github.com/hoophq/libhoop/v2/codec/mssql/auth"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/session"
)

func init() { proxy.RegisterLogin(inspect.MSSQL, loginMSSQL) }

// mssqlIntegratedUser is the passthrough_users entry that lets an
// integrated (Windows, SSPI) login through unchanged. The principal is
// inside the Kerberos or NTLM token, so the relay cannot name the user; the
// operator names the mode instead.
const mssqlIntegratedUser = "integrated"

// loginMSSQL terminates a TDS login on the relay.
//
// Client leg: PRELOGIN names the client's encryption. ENCRYPT_OFF and
// ENCRYPT_ON/REQ both start a TLS handshake carried inside PRELOGIN packets,
// which the relay terminates with its own certificate: under OFF only the
// first LOGIN7 packet is encrypted and the session continues in clear,
// under ON/REQ the whole client leg stays TLS and the pumps read the
// decrypted side. ENCRYPT_NOT_SUP sends LOGIN7 in clear. A TDS 8 client
// (Encrypt=strict) opens with a TLS record instead of a PRELOGIN; the relay
// has no certificate it would accept, so it is refused by name.
//
// Upstream leg: the client's PRELOGIN with ENCRYPT_ON, so the upstream
// session is TLS end to end, then LOGIN7 with the credential: SQL
// authentication, or SSPI driven by cred.GSS when the credential is a
// Kerberos one. The login response goes to the client as the server sent it.
func loginMSSQL(ctx context.Context, c *proxy.LoginConn) (proxy.LoginResult, error) {
	log := c.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	inspectPkt := func(dir inspect.Direction, pkt []byte) error {
		if c.Inspect == nil {
			return nil
		}
		return c.Inspect(dir, pkt)
	}

	// PRELOGIN, or a TDS 8 ClientHello where one should be.
	var head [mssqlauth.HeaderLen]byte
	if _, err := io.ReadFull(c.Client, head[:]); err != nil {
		return proxy.LoginResult{}, fmt.Errorf("mssql login: prelogin: %w", err)
	}
	if mssqlauth.IsTLSRecord(head[:]) {
		return proxy.LoginResult{}, fmt.Errorf("%w: TDS 8 strict encryption (Encrypt=strict) is not supported on this lane; use Encrypt=true or Encrypt=false", proxy.ErrLoginRefused)
	}
	prelogin, err := mssqlReadPacketAfter(head, c.Client)
	if err != nil {
		return proxy.LoginResult{}, fmt.Errorf("mssql login: prelogin: %w", err)
	}
	if prelogin.Type != mssqlauth.PacketPrelogin {
		return proxy.LoginResult{}, fmt.Errorf("mssql login: first packet is %#x, not PRELOGIN", prelogin.Type)
	}
	clientPre, err := mssqlauth.ParsePrelogin(prelogin.Payload)
	if err != nil {
		return proxy.LoginResult{}, fmt.Errorf("mssql login: prelogin: %w", err)
	}
	clientEnc, ok := clientPre.Encryption()
	if !ok {
		return proxy.LoginResult{}, errors.New("mssql login: PRELOGIN without an ENCRYPTION option")
	}

	// The upstream answers the PRELOGIN so the client sees the server's
	// version and options; only ENCRYPTION is the relay's to decide.
	up, err := c.Dial(ctx, nil)
	if err != nil {
		return proxy.LoginResult{}, err
	}
	c.Upstream = up
	upPre := append(mssqlauth.Prelogin(nil), clientPre...)
	upPre.SetEncryption(mssqlauth.EncryptOn)
	upPreBody, err := upPre.Encode()
	if err != nil {
		return proxy.LoginResult{}, fmt.Errorf("mssql login: upstream prelogin: %w", err)
	}
	upPrelogin := mssqlauth.Packet{Type: mssqlauth.PacketPrelogin, Status: mssqlauth.StatusEOM, ID: 1, Payload: upPreBody}.Encode()
	if err := inspectPkt(inspect.FromClient, upPrelogin); err != nil {
		return proxy.LoginResult{}, err
	}
	if _, err := up.Write(upPrelogin); err != nil {
		return proxy.LoginResult{}, fmt.Errorf("mssql login: upstream prelogin: %w", err)
	}
	reply, err := mssqlauth.ReadPacket(up)
	if err != nil {
		return proxy.LoginResult{}, fmt.Errorf("mssql login: upstream prelogin reply: %w", err)
	}
	srvPre, err := mssqlauth.ParsePrelogin(reply.Payload)
	if err != nil {
		return proxy.LoginResult{}, fmt.Errorf("mssql login: upstream prelogin reply: %w", err)
	}
	if srvEnc, _ := srvPre.Encryption(); srvEnc == mssqlauth.EncryptNotSup {
		return proxy.LoginResult{}, errors.New("mssql login: the upstream does not support encryption; the credential would travel in clear")
	}

	// What the client leg gets: its own choice, with REQ answered as ON
	// (both mean the whole session) and ENCRYPTION=strict, which no client
	// sends in a clear PRELOGIN, refused like the TDS 8 ClientHello above.
	answer := clientEnc
	switch clientEnc {
	case mssqlauth.EncryptReq:
		answer = mssqlauth.EncryptOn
	case mssqlauth.EncryptStrict:
		return proxy.LoginResult{}, fmt.Errorf("%w: TDS 8 strict encryption (Encrypt=strict) is not supported on this lane; use Encrypt=true or Encrypt=false", proxy.ErrLoginRefused)
	}
	srvPre.SetEncryption(answer)
	if reply.Payload, err = srvPre.Encode(); err != nil {
		return proxy.LoginResult{}, fmt.Errorf("mssql login: prelogin reply: %w", err)
	}
	clientReply := reply.Encode()
	if err := inspectPkt(inspect.FromServer, clientReply); err != nil {
		return proxy.LoginResult{}, err
	}
	if _, err := c.Client.Write(clientReply); err != nil {
		return proxy.LoginResult{}, fmt.Errorf("mssql login: prelogin reply: %w", err)
	}

	// Login TLS on the client leg. clientIn is where LOGIN7 continuation
	// packets and SSPI packets come from, clientOut where every reply goes;
	// first is where the first LOGIN7 packet comes from, which under
	// ENCRYPT_OFF is the only encrypted one.
	var (
		clientIn  io.Reader = c.Client
		clientOut io.Writer = c.Client
		first     io.Reader = c.Client
	)
	if answer == mssqlauth.EncryptOff || answer == mssqlauth.EncryptOn {
		cfg, err := mssqlauth.LoginTLSConfig()
		if err != nil {
			return proxy.LoginResult{}, err
		}
		framed := mssqlauth.NewFramedConn(c.Client)
		srv := tls.Server(framed, cfg)
		if err := srv.HandshakeContext(ctx); err != nil {
			return proxy.LoginResult{}, fmt.Errorf("mssql login: client TLS handshake: %w", err)
		}
		first = srv
		if answer == mssqlauth.EncryptOn {
			framed.Upgrade()
			clientIn, clientOut = srv, srv
			c.Client = srv
		} else {
			framed.BoundRecords()
		}
	}

	loginBody, err := mssqlReadLogin7(first, clientIn)
	if err != nil {
		return proxy.LoginResult{}, fmt.Errorf("mssql login: login7: %w", err)
	}
	login, err := mssqlauth.DecodeLogin(loginBody)
	if err != nil {
		return proxy.LoginResult{}, fmt.Errorf("mssql login: login7: %w", err)
	}

	clientUser := login.UserName
	user := clientUser
	if login.Integrated() {
		user = mssqlIntegratedUser
	}
	claimed := session.Identity{Subject: clientUser}
	// refuse answers the client with 18456 and returns a refusal. An
	// authenticator's error already wraps proxy.ErrLoginRefused and is kept
	// whole; anything else becomes one.
	refuse := func(reason error) (proxy.LoginResult, error) {
		log.Debug("mssql login refused", "user", clientUser, "integrated", login.Integrated(), "reason", reason)
		_, _ = clientOut.Write(mssqlauth.LoginFailedReply(clientUser))
		if !errors.Is(reason, proxy.ErrLoginRefused) {
			reason = fmt.Errorf("%w: %v", proxy.ErrLoginRefused, reason)
		}
		return proxy.LoginResult{Identity: claimed}, reason
	}

	// Verify first: a refused client costs the upstream nothing.
	passthrough := c.Passthrough != nil && c.Passthrough(user)
	var (
		identity session.Identity
		cred     proxy.Credential
	)
	switch {
	case passthrough:
	case login.Integrated():
		return refuse(errors.New("integrated authentication is not accepted on this lane; list " + mssqlIntegratedUser + " in passthrough_users to relay it"))
	case c.Authenticate == nil:
		return refuse(errors.New("lane has no authenticator"))
	default:
		if identity, cred, err = c.Authenticate(ctx, clientUser, login.Password); err != nil {
			return refuse(err)
		}
	}

	// Upstream login TLS: the upstream answered ON (or OFF/REQ, which with
	// the relay's ON means the whole session), so every byte from here on
	// is a TLS record. A failure from here returns the credential so the
	// relay releases it.
	fail := func(err error) (proxy.LoginResult, error) {
		return proxy.LoginResult{Identity: identity, Credential: cred}, err
	}
	upTLS, err := mssqlUpstreamTLS(ctx, up, c.UpstreamTLS, c.Lane.UpstreamAddr)
	if err != nil {
		return fail(err)
	}
	c.Upstream = upTLS

	if passthrough {
		claimedFail := func(err error) (proxy.LoginResult, error) {
			return proxy.LoginResult{Identity: claimed, Passthrough: true}, err
		}
		pkt := mssqlauth.Frame(mssqlauth.PacketLogin7, loginBody, mssqlauth.DefaultPacketSize)
		if err := inspectPkt(inspect.FromClient, pkt); err != nil {
			return claimedFail(err)
		}
		if _, err := upTLS.Write(pkt); err != nil {
			return claimedFail(fmt.Errorf("mssql login: upstream login7: %w", err))
		}
		if err := mssqlRelayLoginResponse(upTLS, clientIn, clientOut, nil, inspectPkt); err != nil {
			return claimedFail(err)
		}
		return proxy.LoginResult{Identity: claimed, Passthrough: true}, nil
	}

	host, port, err := net.SplitHostPort(c.Lane.UpstreamAddr)
	if err != nil {
		host, port = c.Lane.UpstreamAddr, "1433"
	}
	var gss codectypes.GSSContext
	if cred.GSS != nil {
		gss, err = cred.GSS.Initiate(ctx, "MSSQLSvc/"+host+":"+port)
		if err != nil {
			return fail(fmt.Errorf("mssql login: gss: %w", err))
		}
		token, _, err := gss.Step(nil)
		if err != nil {
			return fail(fmt.Errorf("mssql login: gss: %w", err))
		}
		login.SetIntegrated(token)
	} else {
		login.SetSQLAuth(cred.User, cred.Password)
	}
	login.ServerName = host
	login.DisablePasswordChange()

	loginBody, err = mssqlauth.EncodeLogin(login)
	if err != nil {
		return fail(fmt.Errorf("mssql login: login7: %w", err))
	}
	pkt := mssqlauth.Frame(mssqlauth.PacketLogin7, loginBody, mssqlauth.DefaultPacketSize)
	if err := inspectPkt(inspect.FromClient, pkt); err != nil {
		return fail(err)
	}
	if _, err := upTLS.Write(pkt); err != nil {
		return fail(fmt.Errorf("mssql login: upstream login7: %w", err))
	}
	if err := mssqlRelayLoginResponse(upTLS, nil, clientOut, gss, inspectPkt); err != nil {
		return fail(err)
	}
	log.Debug("mssql login swapped", "user", clientUser, "credential", cred.Name)
	return proxy.LoginResult{Identity: identity, Credential: cred}, nil
}

// mssqlReadPacketAfter finishes reading a packet whose header is in head.
func mssqlReadPacketAfter(head [mssqlauth.HeaderLen]byte, r io.Reader) (mssqlauth.Packet, error) {
	length := int(head[2])<<8 | int(head[3])
	if length < mssqlauth.HeaderLen {
		return mssqlauth.Packet{}, mssqlauth.ErrMalformed
	}
	buf := make([]byte, length)
	copy(buf, head[:])
	if _, err := io.ReadFull(r, buf[mssqlauth.HeaderLen:]); err != nil {
		return mssqlauth.Packet{}, err
	}
	p, _, err := mssqlauth.DecodePacket(buf)
	return p, err
}

// mssqlReadLogin7 reads a LOGIN7 message whose first packet comes from
// first and whose continuation packets, if any, come from rest.
func mssqlReadLogin7(first, rest io.Reader) ([]byte, error) {
	p, err := mssqlauth.ReadPacket(first)
	if err != nil {
		return nil, err
	}
	if p.Type != mssqlauth.PacketLogin7 {
		return nil, fmt.Errorf("packet %#x where LOGIN7 was expected", p.Type)
	}
	body := p.Payload
	for p.Status&mssqlauth.StatusEOM == 0 {
		if p, err = mssqlauth.ReadPacket(rest); err != nil {
			return nil, err
		}
		if p.Type != mssqlauth.PacketLogin7 {
			return nil, fmt.Errorf("packet %#x inside LOGIN7", p.Type)
		}
		if len(body)+len(p.Payload) > mssqlauth.MaxMessageLen {
			return nil, fmt.Errorf("LOGIN7 over %d bytes", mssqlauth.MaxMessageLen)
		}
		body = append(body, p.Payload...)
	}
	return body, nil
}

// mssqlUpstreamTLS runs the TLS handshake inside PRELOGIN packets toward
// the upstream and returns the conn the session continues on. Without a
// lane config the server certificate is not verified: TDS forces the
// handshake whether or not the operator trusts anything, as every driver
// under Encrypt=false does.
func mssqlUpstreamTLS(ctx context.Context, up net.Conn, lane *tls.Config, addr string) (*tls.Conn, error) {
	var cfg *tls.Config
	if lane == nil {
		cfg = &tls.Config{InsecureSkipVerify: true}
	} else {
		cfg = lane.Clone()
		if cfg.ServerName == "" && !cfg.InsecureSkipVerify {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("mssql login: upstream address %q: %w", addr, err)
			}
			cfg.ServerName = host
		}
	}
	// SQL Server expects one TLS record per TDS packet.
	cfg.DynamicRecordSizingDisabled = true
	framed := mssqlauth.NewFramedConn(up)
	tc := tls.Client(framed, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("mssql login: upstream TLS handshake: %w", err)
	}
	framed.Upgrade()
	return tc, nil
}

// mssqlRelayLoginResponse moves the login response to the client, driving
// the SSPI exchange on the way: with gss the relay answers the server's
// tokens itself and the client never sees them; without it (pass-through)
// the client's own SSPI packets from clientIn are relayed. It returns the
// server's ERROR when the upstream refused the login, after the client has
// been told.
func mssqlRelayLoginResponse(
	up io.ReadWriter, clientIn io.Reader, clientOut io.Writer,
	gss codectypes.GSSContext, inspectPkt func(inspect.Direction, []byte) error,
) error {
	for {
		typ, payload, err := mssqlauth.ReadMessage(up)
		if err != nil {
			return fmt.Errorf("mssql login: upstream reply: %w", err)
		}
		if typ != mssqlauth.PacketReply {
			return fmt.Errorf("mssql login: upstream sent %#x where a reply was expected", typ)
		}
		resp, err := mssqlauth.ParseLoginResponse(payload)
		if err != nil {
			return fmt.Errorf("mssql login: upstream reply: %w", err)
		}

		if resp.SSPI != nil && gss != nil {
			out, _, err := gss.Step(resp.SSPI)
			if err != nil {
				return fmt.Errorf("mssql login: gss: %w", err)
			}
			if !resp.LoginAck && resp.Err == nil {
				if len(out) == 0 {
					return errors.New("mssql login: gss produced no token for the server's challenge")
				}
				pkt := mssqlauth.Frame(mssqlauth.PacketSSPI, out, mssqlauth.DefaultPacketSize)
				if err := inspectPkt(inspect.FromClient, pkt); err != nil {
					return err
				}
				if _, err := up.Write(pkt); err != nil {
					return fmt.Errorf("mssql login: upstream sspi: %w", err)
				}
				continue
			}
			payload = resp.StripSSPI(payload)
		}

		pkt := mssqlauth.Frame(mssqlauth.PacketReply, payload, mssqlauth.DefaultPacketSize)
		if err := inspectPkt(inspect.FromServer, pkt); err != nil {
			return err
		}
		if _, err := clientOut.Write(pkt); err != nil {
			return fmt.Errorf("mssql login: reply to client: %w", err)
		}
		switch {
		case resp.Err != nil:
			return fmt.Errorf("mssql login: upstream refused the login: %w", resp.Err)
		case resp.LoginAck:
			return nil
		case resp.SSPI != nil && clientIn != nil:
			// Pass-through integrated login: the client answers.
			cont, err := mssqlauth.ReadPacket(clientIn)
			if err != nil {
				return fmt.Errorf("mssql login: client sspi: %w", err)
			}
			if cont.Type != mssqlauth.PacketSSPI {
				return fmt.Errorf("mssql login: client sent %#x during the SSPI exchange", cont.Type)
			}
			raw := cont.Encode()
			if err := inspectPkt(inspect.FromClient, raw); err != nil {
				return err
			}
			if _, err := up.Write(raw); err != nil {
				return fmt.Errorf("mssql login: upstream sspi: %w", err)
			}
		default:
			return errors.New("mssql login: upstream reply carried neither LOGINACK nor ERROR")
		}
	}
}
