package login

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"

	pgauth "github.com/hoophq/libhoop/v2/codec/postgres/auth"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/session"
)

func init() { proxy.RegisterLogin(inspect.Postgres, loginPostgres) }

// pgInvalidPassword is the SQLSTATE and text psql shows for a wrong
// password. The relay says the same for every refusal, whatever the reason:
// a different message for "no such credential" would let a client enumerate
// users.
const pgInvalidPassword = "28P01"

// loginPostgres terminates a pgwire login on the relay.
//
// Client leg: the StartupMessage names the user. A pass-through user is
// dialed and forwarded unchanged. Any other user is asked for its secret in
// clear, which the relay allows only when the leg cannot be read by a third
// party (unix socket or TLS the relay terminated). The secret never reaches
// the upstream.
//
// Upstream leg: a StartupMessage with the credential's user, the client's
// database, encoding, DateStyle and TimeZone (options and the rest stop
// here) and the propagated application_name, then whatever the server
// asks for: cleartext, MD5, SCRAM, GSSAPI or nothing at all (trust,
// certificate). BackendKeyData is replaced with a pair of the relay's own so
// a CancelRequest, which arrives on a fresh connection, can be mapped back.
func loginPostgres(ctx context.Context, c *proxy.LoginConn) (proxy.LoginResult, error) {
	log := c.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	params, raw, err := pgauth.ReadStartup(c.Client)
	if err != nil {
		return proxy.LoginResult{}, fmt.Errorf("postgres login: startup: %w", err)
	}
	user := params["user"]
	claimed := session.Identity{Subject: user}

	if c.Passthrough != nil && c.Passthrough(user) {
		up, err := c.Dial(ctx, nil)
		if err != nil {
			return proxy.LoginResult{}, err
		}
		c.Upstream = up
		if err := c.Inspect(inspect.FromClient, raw); err != nil {
			return proxy.LoginResult{}, err
		}
		if _, err := up.Write(raw); err != nil {
			return proxy.LoginResult{}, err
		}
		return proxy.LoginResult{Identity: claimed, Passthrough: true}, nil
	}

	refuse := func(reason string) (proxy.LoginResult, error) {
		log.Debug("postgres login refused", "user", user, "reason", reason)
		msg := fmt.Sprintf("password authentication failed for user %q", user)
		_, _ = c.Client.Write(pgauth.ErrorResponse(pgInvalidPassword, msg))
		return proxy.LoginResult{Identity: claimed}, fmt.Errorf("%w: %s", proxy.ErrLoginRefused, msg)
	}

	secret, err := pgClientSecret(c)
	if err != nil {
		if errors.Is(err, proxy.ErrLoginRefused) {
			return refuse(err.Error())
		}
		return proxy.LoginResult{}, err
	}
	if c.Authenticate == nil {
		return refuse("lane has no authenticator")
	}
	identity, cred, err := c.Authenticate(ctx, user, secret)
	if err != nil {
		if errors.Is(err, proxy.ErrLoginRefused) {
			return refuse(err.Error())
		}
		return proxy.LoginResult{}, fmt.Errorf("postgres login: authenticate: %w", err)
	}
	if identity.Subject == "" {
		identity.Subject = user
	}
	// From here the credential is the relay's to release on failure.
	fail := func(err error) (proxy.LoginResult, error) {
		return proxy.LoginResult{Identity: identity, Credential: cred}, err
	}

	up, err := c.Dial(ctx, &cred)
	if err != nil {
		return fail(err)
	}
	c.Upstream = up

	upParams := pgUpstreamParams(params, cred.User)
	if app := c.Propagate.Render(identity, c.Context); app != "" {
		upParams["application_name"] = app
	}
	startup := pgauth.Startup(upParams)
	if err := c.Inspect(inspect.FromClient, startup); err != nil {
		return fail(err)
	}
	if _, err := up.Write(startup); err != nil {
		return fail(err)
	}

	ucred := codectypes.UpstreamCredential{User: cred.User, Password: cred.Password, Cert: cred.Cert, GSS: cred.GSS}
	if err := pgauth.Authenticate(ctx, up, ucred, "postgres/"+pgHost(c.Lane.UpstreamAddr)); err != nil {
		var se *pgauth.ServerError
		if errors.As(err, &se) {
			log.Warn("postgres upstream login failed", "credential", cred.Name, "upstream_user", cred.User, "code", se.Code, "message", se.Message)
		} else {
			log.Warn("postgres upstream login failed", "credential", cred.Name, "upstream_user", cred.User, "err", err)
		}
		_, _ = c.Client.Write(pgauth.ErrorResponse("08006", "upstream authentication failed"))
		return fail(fmt.Errorf("postgres login: upstream: %w", err))
	}

	forward := func(pkt []byte) error {
		if err := c.Inspect(inspect.FromServer, pkt); err != nil {
			return err
		}
		_, err := c.Client.Write(pkt)
		return err
	}
	if err := forward(pgauth.AuthOk()); err != nil {
		return fail(err)
	}
	var cancel *proxy.CancelMapping
	for {
		typ, body, err := pgauth.ReadBackend(up)
		if err != nil {
			return fail(fmt.Errorf("postgres login: after ok: %w", err))
		}
		pkt := pgauth.Message(typ, body)
		switch typ {
		case pgauth.TagBackendKeyData:
			upPID, upKey, err := pgauth.ParseBackendKeyData(body)
			if err != nil {
				return fail(err)
			}
			pid, key, err := randomKeyData()
			if err != nil {
				return fail(err)
			}
			cancel = &proxy.CancelMapping{ClientPID: pid, ClientKey: key, UpstreamPID: upPID, UpstreamKey: upKey}
			pkt = pgauth.BackendKeyData(pid, key)
		case pgauth.TagErrorResponse:
			_ = forward(pkt)
			f := pgauth.ErrorFields(body)
			return fail(fmt.Errorf("postgres login: server error %s: %s", f['C'], f['M']))
		}
		if err := forward(pkt); err != nil {
			return fail(err)
		}
		if typ == pgauth.TagReadyForQuery {
			break
		}
	}
	return proxy.LoginResult{Identity: identity, Credential: cred, Cancel: cancel}, nil
}

// pgUpstreamStartupParams are the client startup parameters a swapped login
// carries upstream. The rest, options above all, could set a role or a GUC
// the credential's boundary does not cover, so they stop here.
var pgUpstreamStartupParams = []string{"database", "application_name", "client_encoding", "DateStyle", "TimeZone"}

// pgUpstreamParams builds the upstream StartupMessage parameters for a
// swapped login: the allowlist from the client's, the credential's user,
// and replication only when the client turned it off.
func pgUpstreamParams(client map[string]string, user string) map[string]string {
	out := make(map[string]string, len(pgUpstreamStartupParams)+2)
	for _, k := range pgUpstreamStartupParams {
		if v, ok := client[k]; ok {
			out[k] = v
		}
	}
	if client["replication"] == "false" {
		out["replication"] = "false"
	}
	out["user"] = user
	if _, ok := out["database"]; !ok {
		// Postgres defaults the database to the login user. The client
		// expected its own name there, not the credential's.
		out["database"] = client["user"]
	}
	return out
}

// pgClientSecret runs the client leg of the login and returns the password
// the client sent in clear. A leg a third party could read is refused
// before anything is asked. A refusal wraps proxy.ErrLoginRefused.
func pgClientSecret(c *proxy.LoginConn) (string, error) {
	if c.Lane.Network != "unix" && !c.Lane.ClientTLS {
		return "", fmt.Errorf("%w: no password method is safe on a plain tcp lane", proxy.ErrLoginRefused)
	}
	if _, err := c.Client.Write(pgauth.AuthCleartext()); err != nil {
		return "", err
	}
	body, err := pgReadPassword(c.Client)
	if err != nil {
		return "", err
	}
	return pgauth.ParsePasswordMessage(body)
}

// pgReadPassword reads the client's next message, which must be a 'p'.
func pgReadPassword(r io.Reader) ([]byte, error) {
	typ, body, err := pgauth.ReadFrontend(r)
	if err != nil {
		return nil, err
	}
	if typ != pgauth.TagPassword {
		return nil, fmt.Errorf("%w: expected a password message, got %q", proxy.ErrLoginRefused, typ)
	}
	return body, nil
}

// randomKeyData returns a pid and key for the client's BackendKeyData.
// Postgres pids are positive; the key is any 32 bits.
func randomKeyData() (pid, key int32, err error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, 0, err
	}
	pid = int32(binary.BigEndian.Uint32(b[:4])&0x7fffffff) | 1
	key = int32(binary.BigEndian.Uint32(b[4:]))
	return pid, key, nil
}

// pgHost is the host part of the upstream address, for the GSS SPN.
func pgHost(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
