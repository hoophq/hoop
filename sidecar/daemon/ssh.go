package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
	codecssh "github.com/hoophq/libhoop/v2/codec/ssh"
)

// An ssh lane is not a relay lane (ADR-0015). It TERMINATES the SSH
// handshake, so every statement it reports carries a principal it verified
// itself rather than one a component upstream claimed. libhoop owns the
// mechanics — handshake, certificate trust, channel dispatch, process spawn,
// file-transfer decoding, the forward dial; this package supplies the
// decisions: what the lane admits, which OS account a session becomes, where
// a forward may reach, and the gate, policy, audit and masking behind them.

// isSSH reports whether a listener is an ssh lane.
//
// It has the same shape as isGRPC and the same job: SSH registers no codec,
// so inspect.New cannot answer for the protocol and every site that would
// ask it needs this carve-out instead. The reason differs, though, and the
// difference is worth keeping in view. A grpc lane has plaintext bytes a
// registry decoder COULD read and enters above them by choice; an ssh lane
// has none to read at all. That is why there is no isSSHTransport twin: gRPC
// grew one because spanner shares its transport, and nothing shares SSH's.
func isSSH(lc ListenerConfig) bool {
	return inspect.Protocol(lc.Protocol) == inspect.SSH
}

// SSHServer is the running side of an ssh lane. Serve blocks until the
// context ends or the listener fails; Close is idempotent.
//
// The same shape as GRPCServer, and for the same reason: the daemon owns the
// lifecycle loop and must not know which of the two it is holding.
type SSHServer interface {
	Serve(ctx context.Context) error
	Close() error
	Addr() net.Addr
	Stats() (active, total, denied int64)
	// Notes returns what this listener admits, for -validate. It must not
	// expose session content, and there is none to expose.
	Notes() []string
}

// buildSSHServer resolves one ssh lane and builds the libhoop endpoint with
// sidecar-owned callbacks.
//
// Everything decided here is decided ONCE, at build: the capability set, the
// destination list, the account. A connection's OpenFunc reads resolved
// values and never re-parses config text, so a typo cannot become a user's
// problem on the hot path.
func buildSSHServer(
	ln lane,
	ac AuditConfig,
	sink audit.Sink,
	log *slog.Logger,
) (SSHServer, error) {
	lc := ln.cfg
	sc := lc.SSH
	if sc == nil {
		// Validate refuses this, so reaching it means a Config assembled in
		// Go rather than loaded from a file. Loud, not nil: a lane with no
		// host key and no trusted CA would refuse everyone and be unable to
		// say why.
		return nil, fmt.Errorf("%s: protocol is ssh but there is no ssh block", ln.name)
	}

	destinations, err := parseSSHDestinations(sc.DestinationsAllowed)
	if err != nil {
		return nil, fmt.Errorf("%s: ssh.destinations_allowed: %w", ln.name, err)
	}

	// A lane that admits no session capability forwards and nothing else:
	// destinations_allowed carries the jump, and this flag drops the shell.
	// Resolved once here rather than per connection, because it is a
	// property of the config and not of the client.
	noSession := sc.admitsNothing()

	laneLog := log.With("listener", ln.name)
	failOnAuditError := ac.failOnAuditError()
	stmts := sshStatements{}

	// libhoop convention: configuration travels as a map[string]string the
	// server validates, with unknown keys refused. Key material rides as
	// file paths, so the server loads it and a bad path fails before
	// binding.
	opts := map[string]string{
		"name":       ln.name,
		"listen":     lc.Listen,
		"host_key":   sc.HostKey,
		"trusted_ca": sc.TrustedCA,
		// Required even when it resolves to nothing. The tri-state lives in
		// the config file and is resolved above; libhoop is deliberately
		// unable to guess a default, so an empty value here means "no
		// session" and a missing key is a construction error.
		"capabilities": joinCapabilities(sc.resolveCapabilities()),
	}
	if lc.Network != "" {
		opts["network"] = lc.Network
	}
	if lc.IdleTimeoutSec > 0 {
		opts["idle_timeout_sec"] = strconv.Itoa(lc.IdleTimeoutSec)
	}
	if lc.MaxConns > 0 {
		opts["max_conns"] = strconv.Itoa(lc.MaxConns)
	}
	if ln.masker != nil {
		opts["mask_output"] = "true"
	}

	open := func(ctx context.Context, info codecssh.ConnInfo) (*codecssh.ConnHandler, *codecssh.Refusal, error) {
		identity := sshIdentity(sc.Identity, info)
		identity.PeerAddr = info.RemoteAddr

		sess := session.New(inspect.SSH, identity)
		sess.Connection = ln.name
		g, err := gate.NewStatementGate(sess, gate.Config{
			Protocol:         inspect.SSH,
			Policy:           ln.policy,
			Audit:            sink,
			Masker:           ln.masker,
			FailOnAuditError: failOnAuditError,
		})
		if err != nil {
			return nil, nil, err
		}

		state := &sshConnState{
			gate:         g,
			stmts:        stmts,
			destinations: destinations,
			log:          laneLog,
		}
		handler := state.callbacks()

		// The account is resolved BEFORE anything is admitted, and a
		// session that cannot name one is refused. There is no default
		// account and no fallback to the user the sidecar happens to run
		// as: either would hand a session an account nobody chose for it.
		//
		// A LANE THAT ADMITS NO SESSION is the exception, and it has to be.
		// Nothing is ever spawned there, which leaves the login name as the
		// only account it could resolve, on a host where that name has no
		// reason to exist. Requiring one would refuse every jump through a
		// shell-less jump host. RunAs stays the zero value, which refuses a
		// session across the seam; such a lane has none, and forwarding
		// never reads it.
		if !noSession {
			runAs, refusal := resolveSessionAccount(info.LoginName)
			if refusal != nil {
				// Returned with the handler, not instead of it: libhoop
				// closes a handler even when the connection is refused, so
				// the audit session that started here still gets its Close.
				return handler, refusal, nil
			}
			handler.RunAs = runAs
		}

		if err := g.Start(ctx); err != nil {
			if failOnAuditError {
				return handler, codecssh.Refuse("audit trail unavailable; connection refused"), nil
			}
			laneLog.Warn("ssh session start not recorded", "error", err)
		}
		return handler, nil, nil
	}

	srv, err := codecssh.NewServer(opts, open, laneLog)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ln.name, err)
	}
	return srv, nil
}

// resolveSessionAccount picks the OS account one connection's sessions run
// as: the login name the client asked for, looked up in the OS user database.
//
// That name is already verified against the certificate's principals, so it
// is the one name this connection can be trusted to become — and it REFUSES
// when it resolves to no OS user.
//
// The refusal is the point. A login name that is not an account could be
// served by falling back to something, and every candidate is wrong: the
// sidecar's own account hands a session to whoever the process happens to
// be, and a fixed default makes the principals check decorative. This is
// the rule sshd has, arrived at for the same reason.
func resolveSessionAccount(loginName string) (codecssh.RunAs, *codecssh.Refusal) {
	if loginName == "" {
		return codecssh.RunAs{}, codecssh.Refuse(
			"this connection named no login, and there is no account to use instead")
	}
	runAs, err := resolveRunAs(loginName)
	if err != nil {
		return codecssh.RunAs{}, codecssh.Refuse(
			"login %q is not an account on this host", loginName)
	}
	if err := canBecome(runAs); err != nil {
		return codecssh.RunAs{}, codecssh.Refuse(
			"this host cannot run a session as %q", loginName)
	}
	return runAs, nil
}

// joinCapabilities renders the resolved set for the options map. An empty set
// renders as the empty string, admitting no session; the KEY is still present,
// which is what tells libhoop the tri-state was resolved.
func joinCapabilities(caps []codecssh.Capability) string {
	names := make([]string, 0, len(caps))
	for _, c := range caps {
		names = append(names, string(c))
	}
	return strings.Join(names, ",")
}

// sshConnState holds one connection's gate and the facts its callbacks read.
type sshConnState struct {
	gate         *gate.Gate
	stmts        sshStatements
	destinations []sshDestination
	log          *slog.Logger
}

func (c *sshConnState) callbacks() *codecssh.ConnHandler {
	h := &codecssh.ConnHandler{
		Exec:    c.exec,
		EnvSet:  c.envSet,
		PTY:     c.pty,
		SFTPOp:  c.sftpOp,
		Forward: c.forward,
		Event:   c.event,
		Close:   c.close,
	}
	// The rewrite hooks are wired only when this lane masks. Nil is not the
	// same as an identity function on the libhoop side: the file-transfer
	// subsystem STREAMS a transfer when there is no hook and buffers it
	// whole when there is one, so an identity hook would make every
	// download and upload pay for masking that does nothing.
	if c.gate.Masker() != nil {
		h.StreamData = c.streamData
		h.SFTPData = c.sftpData
	}
	return h
}

func (c *sshConnState) exec(ctx context.Context, cmd string) *codecssh.Refusal {
	return c.judge(ctx, "exec", c.stmts.exec(cmd))
}

func (c *sshConnState) envSet(ctx context.Context, name, value string) *codecssh.Refusal {
	return c.judge(ctx, "env", c.stmts.envSet(name, value))
}

// pty admits, and records NOTHING of its own.
//
// A terminal request carries geometry and no content, so there is nothing
// for a rule to read: libhoop has already checked the capability and the
// certificate's permit-pty. The geometry is not lost — it rides on the
// session's own close record, because a pty is not a session, it is a
// property of one (ADR-0015). A separate event here would put two rows in
// the trail for one thing that happened.
//
// It must still be wired. A nil callback REFUSES the thing it governs, so
// leaving this out would deny every interactive session on a lane that
// admits pty.
func (c *sshConnState) pty(ctx context.Context, term string, cols, rows int) *codecssh.Refusal {
	return nil
}

// sftpOp judges one file operation, and BOTH ENDS of a two-path request.
//
// A rename out of a fenced directory and a rename into one are different
// questions. Evaluating only the source would let `rename /tmp/x /etc/passwd`
// past a rule written to fence /etc, which is the kind of miss that makes a
// guardrail worse than none.
func (c *sshConnState) sftpOp(ctx context.Context, op inspect.Operation, path, target string) *codecssh.Refusal {
	end := ""
	if target != "" {
		end = sshPathEndSource
	}
	if r := c.judge(ctx, "sftp", c.stmts.sftp(op, path, target, end)); r != nil {
		return r
	}
	if target == "" {
		return nil
	}
	return c.judge(ctx, "sftp", c.stmts.sftp(op, target, path, sshPathEndTarget))
}

// forward checks a client-opened forward against destinations_allowed.
//
// It gets no statement, deliberately. A forward carries bytes this lane
// cannot read — no protocol knowledge, no decoder — so there is nothing for
// a rule to evaluate, and a statement built from a destination would be a
// rule surface that looks like content policy and is not. The destination
// list is the whole control, and it is default-deny: an absent or empty list
// denies every forward.
//
// dialing is the address libhoop WILL connect to, resolved once. Checking it
// rather than the requested name is what closes the resolve-twice window.
func (c *sshConnState) forward(ctx context.Context, req codecssh.Destination, dialing netip.AddrPort) *codecssh.Refusal {
	for _, d := range c.destinations {
		if d.Allows(dialing) {
			return nil
		}
	}
	c.event(ctx, "forward_denied", map[string]string{
		"destination": req.String(),
		"dialing":     dialing.String(),
	})
	return codecssh.Refuse(
		"this listener does not carry forwards to %s", req.String())
}

// judge runs one statement through the gate and turns a denial into the
// client-facing refusal. The gate is where guardrails, OPA, the analyzer,
// audit and the counters already live, so this adds no second decision path.
func (c *sshConnState) judge(ctx context.Context, phase string, stmt inspect.Statement) *codecssh.Refusal {
	d := c.gate.EvaluateStatement(ctx, stmt)
	if d.Err != nil {
		c.log.Warn("ssh statement evaluation continued after error",
			"phase", phase, "error", d.Err)
	}
	if !d.Allowed {
		return codecssh.Refuse("%s", d.Message)
	}
	return nil
}

// streamData rewrites session bytes in flight. It is a REWRITE hook and not
// a capture hook: what it returns is forwarded and dropped, and neither
// version is kept anywhere (ADR-0015, Audit granularity).
//
// Only server-to-client bytes are scanned. A mask rule protects what the
// session SEES; rewriting what the user typed would change the command that
// runs, which is a denial wearing a redaction's clothes — and the input side
// already has guardrails, which refuse rather than silently alter.
func (c *sshConnState) streamData(ctx context.Context, dir inspect.Direction, b []byte) ([]byte, *codecssh.Refusal) {
	if dir != inspect.FromServer {
		return b, nil
	}
	return c.mask(ctx, "terminal output", b)
}

// sftpData rewrites file bytes. libhoop decides what a rewrite MEANS per
// direction — a download is served masked, an upload that comes back changed
// is refused with nothing landing — so this side only has to mask and
// preserve the length.
func (c *sshConnState) sftpData(ctx context.Context, path string, b []byte) ([]byte, *codecssh.Refusal) {
	return c.mask(ctx, "file transfer", b)
}

// mask applies the lane's rules and ENFORCES length preservation.
//
// The config refuses every variable-length strategy at load, so a mismatch
// here means the masker did something its configuration said it would not.
// That is exactly when a comment is not enough: the bytes would still be
// forwarded, shifted, and the client would read a corrupted stream rather
// than an error. So it fails the stream CLOSED — nothing is forwarded and
// the refusal says why.
func (c *sshConnState) mask(ctx context.Context, what string, b []byte) ([]byte, *codecssh.Refusal) {
	masker := c.gate.Masker()
	if masker == nil || len(b) == 0 {
		return b, nil
	}
	out, entities, count := masker.Mask(b)
	if count == 0 {
		return b, nil
	}
	if len(out) != len(b) {
		c.log.Error("ssh masking changed the length of a stream; refusing it",
			"stream", what, "in", len(b), "out", len(out))
		c.event(ctx, "mask_failed", map[string]string{
			"stream": what,
			"reason": "the rewrite changed the byte length of the stream",
		})
		return nil, codecssh.Refuse(
			"masking could not be applied to this %s without changing its length; "+
				"the stream was closed rather than corrupted", what)
	}
	if err := c.gate.RecordMasked(ctx, entities, count); err != nil {
		c.log.Warn("ssh masking not recorded", "error", err)
	}
	return out, nil
}

// close records the connection's totals, then ends the audit session.
//
// The totals are the point. With no content trail, an admitted shell's whole
// audit record is its open, the geometry its pty contributed, its duration
// and these counts — so they are load-bearing rather than decoration, and
// they are the one thing this callback must not drop. They ride on their own
// record because the session-end event counts STATEMENTS, and a shell
// produces none: a session that shows "0 statements" and nothing else would
// read as a connection that did nothing, when it may have been four minutes
// of someone's terminal.
func (c *sshConnState) close(ctx context.Context, s codecssh.Stats) error {
	ctx = context.WithoutCancel(ctx)

	attrs := map[string]string{
		"bytes_in":    strconv.FormatInt(s.BytesIn, 10),
		"bytes_out":   strconv.FormatInt(s.BytesOut, 10),
		"duration_ms": strconv.FormatInt(s.Duration.Milliseconds(), 10),
	}
	if s.ExitCode != nil {
		attrs["exit_code"] = strconv.Itoa(*s.ExitCode)
	}
	c.event(ctx, "connection_close", attrs)

	err := c.gate.Close(ctx)
	if err != nil {
		c.log.Warn("ssh session end not recorded", "error", err)
	}
	return err
}

// event records something worth keeping that is not a statement: a
// capability admitted or refused, a terminal's geometry, a forward carried
// or denied.
//
// attrs is flat metadata and never content. v1 keeps no session content at
// all (ADR-0015, Audit granularity), so what reaches the trail from a shell
// is its open, its geometry, its duration and its byte counts — which makes
// these records the whole audit story for the one capability that has no
// statements.
func (c *sshConnState) event(ctx context.Context, kind string, attrs map[string]string) {
	if err := c.gate.RecordActivity(ctx, kind, attrs); err != nil {
		c.log.Warn("ssh activity not recorded", "activity", kind, "error", err)
	}
}

// sshIdentity maps a verified certificate onto the session identity policy
// and audit read.
//
// A certificate has no field called "email", so this is a mapping and not a
// read. The default is the key id as the subject: it is the field a CA
// controls per issued certificate, and an audit trail with no subject cannot
// say who ran a command.
//
// It reads the certificate's FIELDS and never names its type. Naming
// *ssh.Certificate would make golang.org/x/crypto a second direct dependency
// of a module whose whole shape is one, and the crypto library belongs on
// libhoop's side of the seam. Go needs the import to name a type, not to
// reach through a value, so the three claims travel as a string, a list and a
// map.
func sshIdentity(cfg *SSHIdentityConfig, info codecssh.ConnInfo) session.Identity {
	id := session.Identity{}
	if info.Cert == nil {
		return id
	}
	keyID, principals, extensions := info.Cert.KeyId, info.Cert.ValidPrincipals, info.Cert.Extensions

	subjectSource := identitySourceKeyID
	if cfg != nil && cfg.Subject != "" {
		subjectSource = cfg.Subject
	}
	id.Subject = certScalar(subjectSource, keyID, principals, extensions)

	if cfg != nil {
		if cfg.Email != "" {
			id.Email = certScalar(cfg.Email, keyID, principals, extensions)
		}
		if cfg.Groups != "" {
			id.Groups = certList(cfg.Groups, keyID, principals, extensions)
		}
		for _, name := range cfg.Attributes {
			value, ok := certExtension(extensions, name)
			if !ok {
				continue
			}
			if id.Attributes == nil {
				id.Attributes = map[string]string{}
			}
			id.Attributes[name] = value
		}
	}
	return id
}

// certScalar reads one certificate field as a single value.
func certScalar(source, keyID string, principals []string, extensions map[string]string) string {
	switch {
	case source == identitySourceKeyID:
		return keyID
	case source == identitySourcePrincipals:
		// The FIRST principal. A scalar slot holds one name, and joining
		// the list would produce a subject no audit query matches.
		if len(principals) > 0 {
			return principals[0]
		}
		return ""
	case strings.HasPrefix(source, identitySourceExtPrefix):
		v, _ := certExtension(extensions, strings.TrimPrefix(source, identitySourceExtPrefix))
		return v
	}
	return ""
}

// certList reads one certificate field as a list, for the groups slot.
func certList(source, keyID string, principals []string, extensions map[string]string) []string {
	switch {
	case source == identitySourceKeyID:
		if keyID == "" {
			return nil
		}
		return []string{keyID}
	case source == identitySourcePrincipals:
		return principals
	case strings.HasPrefix(source, identitySourceExtPrefix):
		v, ok := certExtension(extensions, strings.TrimPrefix(source, identitySourceExtPrefix))
		if !ok || v == "" {
			return nil
		}
		return splitAndTrim(v)
	}
	return nil
}

// certExtension reads one extension, reporting presence separately from
// value.
//
// An OpenSSH flag extension carries an EMPTY value: permit-pty is present or
// absent, and its value is never read. Rendering presence as "true" is what
// lets a policy read input.context["permit-pty"] the way it reads any other
// attribute, instead of seeing an empty string that looks like absence.
func certExtension(extensions map[string]string, name string) (string, bool) {
	v, ok := extensions[name]
	if !ok {
		return "", false
	}
	if v == "" {
		return "true", true
	}
	return v, true
}

// splitAndTrim turns a comma-separated extension value into a list.
func splitAndTrim(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// endpointServer is what a lane that TERMINATES its protocol in-process
// looks like to the daemon's lifecycle: the grpc transport (ADR-0013) and
// the ssh endpoint (ADR-0015) both satisfy it.
//
// It is unexported because it is an internal join, not a contract: GRPCServer
// and SSHServer stay the exported shapes an embedder implements. What this
// buys is one serve loop, one error fan-in, one shutdown loop and one stats
// zip for every endpoint protocol instead of a pair of parallel slices per
// protocol, each of which has to stay in lockstep with a name list.
type endpointServer interface {
	Serve(ctx context.Context) error
	Close() error
	Addr() net.Addr
	Stats() (active, total, denied int64)
	Notes() []string
}

// sshLaneNotes reports the facts a -validate run has to state out loud but
// libhoop cannot: where this listener will carry a forward, and which OS
// account its sessions become.
//
// Both are decisions this side of the seam owns, and both are the kind of
// thing an operator believes they configured correctly right up until a
// session proves otherwise. The capability set, the CA and the host key come
// from the endpoint's own Notes, so they are not repeated here.
func sshLaneNotes(sc *SSHConfig) []string {
	if sc == nil {
		return nil
	}
	var notes []string

	switch {
	case len(sc.DestinationsAllowed) == 0:
		// Said explicitly, because "no destinations" is the DEFAULT and a
		// listener configured without the key looks complete until the
		// first forward is refused. A lane that admits no session is the
		// urgent case: forwarding is the only thing it could have been for.
		note := "ssh: destinations_allowed is empty, so every client-opened forward is denied"
		if sc.admitsNothing() {
			note += ", and this listener admits no session either, so it carries nothing at all"
		}
		notes = append(notes, note)
	default:
		notes = append(notes, fmt.Sprintf(
			"ssh: carries forwards to %s, and nowhere else",
			strings.Join(sc.DestinationsAllowed, ", ")))
	}

	if !sc.admitsNothing() {
		notes = append(notes,
			"ssh: every session runs as the login name the certificate admitted, and "+
				"a login that is not an account on this host is refused")
	}
	// sftp is the other account fact worth stating at load — it is served
	// in this process, so it reaches one account and no other — and the
	// endpoint's own Notes already say it. Repeating it here would print it
	// twice.
	return notes
}

// isEndpointLane reports whether a lane's policy stack is CAPTURED by a
// running server rather than swappable underneath it.
//
// An endpoint lane builds its evaluator once and closes over it, so a hot
// reload cannot move the rules without rebuilding the server. Both the grpc
// transport and the ssh endpoint work this way, and the reload path has to
// treat them alike: swapping the view lane while the serving closure keeps
// the old evaluator would leave an operator looking at rules that are not
// the rules being enforced, with nothing saying so.
func isEndpointLane(lc ListenerConfig) bool {
	return isGRPCTransport(lc) || isSSH(lc)
}
