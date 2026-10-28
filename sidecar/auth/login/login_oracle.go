package login

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"

	codecoracle "github.com/hoophq/libhoop/v2/codec/oracle"
	"github.com/hoophq/libhoop/v2/codec/oracle/oranet"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/session"
)

func init() { proxy.RegisterLogin(inspect.Oracle, loginOracle) }

// oraLogonDenied is ORA-01017, what Oracle says for a wrong password. The
// relay says the same for every refusal, so a client cannot tell a missing
// verifier from a wrong secret.
const (
	oraLogonDenied     = 1017
	oraLogonDeniedText = "invalid username/password; logon denied"
)

// oracleConnectAttempts bounds the CONNECT round trips a RESEND can cause.
const oracleConnectAttempts = 3

// errOracleNoVerifier is why every non-pass-through Oracle login is refused.
//
// O5LOGON is a mutual proof: the client's secret never crosses the wire. The
// server encrypts its session key under a key derived from the password
// (verifier 18453: AES-256 with SHA-512(PBKDF2-HMAC-SHA512(password,
// salt||"AUTH_PBKDF2_SPEEDY_KEY", vgen) || salt)[:32]); the client answers
// with its own session key and the password under the same derivation.
// Nothing in the exchange opens without that key, and no verifier the
// sidecar links holds it: a token is checked, never derived from. The agent
// proxy re-keys only because hoop's CLI tells it the placeholder password
// the client uses.
//
// What a follow-up needs, per user, so the relay can be the O5LOGON server
// on the client leg and recover the secret in clear:
//
//	Salt      [16]byte  random, AUTH_VFR_DATA of the challenge the relay sends
//	VgenCount int       4096, AUTH_PBKDF2_VGEN_COUNT
//	Key       [32]byte  SHA-512(PBKDF2-HMAC-SHA512(secret, Salt||"AUTH_PBKDF2_SPEEDY_KEY", VgenCount) || Salt)[:32]
//
// That is the 12c "T:" verifier Oracle itself stores. With it the relay
// decrypts the client's AUTH_SESSKEY and AUTH_PASSWORD (oraauth.DeriveUserKey
// and decryptSessKey with Key in place of the derived key), passes the
// secret to Authenticate unchanged, logs in upstream with a session key of
// its own, and rewrites AUTH_SVR_RESPONSE in the accept RPA so OCI clients
// verify.
var errOracleNoVerifier = errors.New("oracle O5LOGON needs a server-side verifier this sidecar does not hold for this lane")

// loginOracle relays the TNS handshake until the client names its user.
//
// CONNECT and ACCEPT (RESEND and REFUSE included), Advanced Networking and
// the protocol and data-type negotiations pass unchanged in strict
// request/response order; every packet goes through c.Inspect. A pass-through
// user's first authentication call is forwarded and the pumps carry the
// rest. Any other user is refused with an ORA-01017 summary in the format
// the session negotiated; see errOracleNoVerifier for why no user can be
// verified yet. In fast authentication (Oracle 23ai) the user arrives
// before the server settles that format, so the bundle is relayed and the
// refusal answers the client's credential call instead.
func loginOracle(ctx context.Context, c *proxy.LoginConn) (proxy.LoginResult, error) {
	log := c.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	up, err := c.Dial(ctx, nil)
	if err != nil {
		return proxy.LoginResult{}, err
	}
	c.Upstream = up

	// track mirrors the gate's codec so the driver knows the login user and
	// the session format without reaching into the gate.
	track := codecoracle.New(codecoracle.Options{})
	observe := func(dir inspect.Direction, pkt []byte) error {
		if err := c.Inspect(dir, pkt); err != nil {
			return err
		}
		_, n, err := track.Decode(dir, pkt)
		if err != nil {
			return fmt.Errorf("oracle login: %s packet: %w", dir, err)
		}
		if n != len(pkt) {
			return fmt.Errorf("oracle login: %s packet of %d bytes decoded only %d", dir, len(pkt), n)
		}
		return nil
	}
	relay := func(dir inspect.Direction, pkt []byte, to net.Conn) error {
		if err := observe(dir, pkt); err != nil {
			return err
		}
		_, err := to.Write(pkt)
		return err
	}

	big, err := oracleConnect(c.Client, up, relay)
	if err != nil {
		return proxy.LoginResult{}, err
	}

	deferred := false
	for {
		pkt, err := oranet.Decode(c.Client, big)
		if err != nil {
			return proxy.LoginResult{}, fmt.Errorf("oracle login: client: %w", err)
		}
		if err := observe(inspect.FromClient, pkt.Encoded()); err != nil {
			return proxy.LoginResult{}, err
		}
		user := track.LoginUser()
		if user == "" {
			if err := oracleRoundTrip(pkt.Encoded(), up, c.Client, big, relay); err != nil {
				return proxy.LoginResult{}, err
			}
			continue
		}
		claimed := session.Identity{Subject: user}
		// Oracle folds an unquoted name to upper case; the operator may
		// have listed either form.
		if c.Passthrough != nil && (c.Passthrough(user) || c.Passthrough(strings.ToUpper(user))) {
			if _, err := up.Write(pkt.Encoded()); err != nil {
				return proxy.LoginResult{}, err
			}
			return proxy.LoginResult{Identity: claimed, Passthrough: true}, nil
		}
		frame := track.ErrorFrame(oraLogonDenied, oraLogonDeniedText)
		if frame == nil {
			if deferred {
				return proxy.LoginResult{}, errors.New("oracle login: session format unknown after the server's reply")
			}
			// Fast authentication: the server's reply settles the format
			// the refusal must take.
			deferred = true
			if err := oracleRoundTrip(pkt.Encoded(), up, c.Client, big, relay); err != nil {
				return proxy.LoginResult{}, err
			}
			continue
		}
		log.Warn("oracle login refused", "user", user, "reason", errOracleNoVerifier.Error())
		_, _ = c.Client.Write(frame)
		return proxy.LoginResult{Identity: claimed}, fmt.Errorf("%w: %v", proxy.ErrLoginRefused, errOracleNoVerifier)
	}
}

// oracleConnect relays the client's CONNECT, a descriptor continuation when
// the packet could not hold it, and the server's answer. A RESEND goes back
// to the client, which sends its CONNECT again. It returns whether the
// accepted version switches TNS to 32-bit packet lengths.
func oracleConnect(client, up net.Conn, relay func(inspect.Direction, []byte, net.Conn) error) (bool, error) {
	for attempt := 1; ; attempt++ {
		pkt, err := oranet.Decode(client, false)
		if err != nil {
			return false, fmt.Errorf("oracle login: client CONNECT: %w", err)
		}
		if pkt.Type() != oranet.PacketConnect {
			return false, fmt.Errorf("oracle login: expected CONNECT, got packet type %d", pkt.Type())
		}
		if err := relay(inspect.FromClient, pkt.Encoded(), up); err != nil {
			return false, err
		}
		if oracleConnectContinues(pkt.Encoded()) {
			rest, err := oranet.Decode(client, false)
			if err != nil {
				return false, fmt.Errorf("oracle login: CONNECT continuation: %w", err)
			}
			if rest.Type() != oranet.PacketData {
				return false, fmt.Errorf("oracle login: expected the CONNECT descriptor, got packet type %d", rest.Type())
			}
			if err := relay(inspect.FromClient, rest.Encoded(), up); err != nil {
				return false, err
			}
		}
		resp, err := oranet.Decode(up, false)
		if err != nil {
			return false, fmt.Errorf("oracle login: upstream CONNECT reply: %w", err)
		}
		switch resp.Type() {
		case oranet.PacketAccept:
			if err := relay(inspect.FromServer, resp.Encoded(), client); err != nil {
				return false, err
			}
			return resp.AcceptVersion() >= oranet.Version315, nil
		case oranet.PacketResend:
			if attempt >= oracleConnectAttempts {
				return false, fmt.Errorf("oracle login: upstream asked for the CONNECT %d times", attempt)
			}
			if err := relay(inspect.FromServer, resp.Encoded(), client); err != nil {
				return false, err
			}
		case oranet.PacketRefuse:
			if err := relay(inspect.FromServer, resp.Encoded(), client); err != nil {
				return false, err
			}
			return false, errors.New("oracle login: upstream refused the connection")
		default:
			// A REDIRECT would send the client around the relay; the codec
			// refuses it and so does the driver.
			return false, fmt.Errorf("oracle login: unexpected CONNECT reply, packet type %d", resp.Type())
		}
	}
}

// oracleConnectContinues reports whether a CONNECT packet's descriptor is
// longer than the packet: the client sends the rest in a DATA packet before
// the server answers.
func oracleConnectContinues(frame []byte) bool {
	if len(frame) < 28 {
		return false
	}
	size := int(binary.BigEndian.Uint16(frame[24:26]))
	offset := int(binary.BigEndian.Uint16(frame[26:28]))
	return offset <= len(frame) && size > len(frame)-offset
}

// oracleRoundTrip forwards one client packet, already inspected, and relays
// the server's one reply. Before the client's credential call the exchange
// is strictly alternating: ANO, protocol and data-type negotiation each
// answer with a single packet, as every captured session shows.
func oracleRoundTrip(pkt []byte, up, client net.Conn, big bool, relay func(inspect.Direction, []byte, net.Conn) error) error {
	if _, err := up.Write(pkt); err != nil {
		return err
	}
	resp, err := oranet.Decode(up, big)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("oracle login: upstream closed during negotiation")
		}
		return fmt.Errorf("oracle login: upstream: %w", err)
	}
	return relay(inspect.FromServer, resp.Encoded(), client)
}
