package daemon

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

// laneAuth is one lane's login termination: the verifier of the client leg,
// the pool, the rule that picks an entry, and the sources that materialize
// it. buildLanes builds it for every lane with a pool; buildServer and the
// endpoint lanes compose the relay's hooks from it.
type laneAuth struct {
	lane         string
	proto        inspect.Protocol
	identityType string
	verifier     IdentitySource
	// scram is the SCRAM material lookup a hoop_credential lane offers a
	// driver; nil on every other verifier.
	scram   func(user string) (codectypes.SCRAMMaterial, bool)
	pool    []CredentialEntry
	sources map[string]CredentialSource
	rules   []CredentialRule
	// opa is the lane's decision endpoint when select.opa names the
	// credential; nil under rules.
	opa     *policy.OPAClient
	context map[string]string
	peer    PeerInfo
	log     *slog.Logger
}

// hasPool reports whether a lane terminates its login.
func (l ListenerConfig) hasPool() bool {
	return l.Credentials != nil && len(l.Credentials.Pool) > 0
}

// identityType is the verifier a lane runs: the configured one, or none on
// a unix socket with a pool and no identity block.
func (l ListenerConfig) identityType() string {
	if l.Identity != nil {
		return l.Identity.Type
	}
	if l.hasPool() && l.Network == "unix" {
		return IdentityNone
	}
	return ""
}

// buildLaneAuth builds a lane's login termination, or nil for a pass-through
// lane. Every error is a config fact: an unlinked type, a source the pool
// names and the block does not configure, a bad template. It logs through
// the process logger, which Run installs as the default before this runs.
func buildLaneAuth(cfg *Config, lc ListenerConfig, name string, roots *x509.CertPool,
	creds *credentialSet, sources map[string]CredentialSource) (*laneAuth, error) {
	if !lc.hasPool() {
		return nil, nil
	}
	typ := lc.identityType()
	build, ok := registeredIdentitySource(typ)
	if !ok {
		return nil, fmt.Errorf("identity type %q is not linked into this build", typ)
	}
	var idCfg IdentityConfig
	if lc.Identity != nil {
		idCfg = *lc.Identity
	}
	laneLog := slog.Default().With("listener", name)
	verifier, err := build(idCfg, IdentityDeps{Roots: roots, Log: laneLog, lane: name, credentials: creds})
	if err != nil {
		return nil, fmt.Errorf("identity %s: %w", typ, err)
	}
	a := &laneAuth{
		lane:         name,
		proto:        inspect.Protocol(lc.Protocol),
		identityType: typ,
		verifier:     verifier,
		pool:         lc.Credentials.Pool,
		sources:      map[string]CredentialSource{},
		rules:        lc.Credentials.Select.Rules,
		context:      lc.Context,
		peer:         PeerInfo{Network: lc.network(), TLS: lc.DownstreamTLS != nil},
		log:          laneLog,
	}
	if h, ok := verifier.(*hoopCredentialIdentity); ok {
		a.scram = h.scramVerifier()
	}
	for _, e := range lc.Credentials.Pool {
		src, ok := sources[e.Source]
		if !ok {
			return nil, fmt.Errorf("credential %q names source %q, which has no credentials.%s block", e.Name, e.Source, e.Source)
		}
		a.sources[e.Source] = src
	}
	if lc.Credentials.Select.OPA {
		_, opa, _ := cfg.resolve(lc)
		if !opa.enabled() {
			return nil, errors.New("credentials.select.opa needs an opa.url on this lane")
		}
		a.opa = opa.client("")
	}
	return a, nil
}

// network is the lane's transport, tcp when unset.
func (l ListenerConfig) network() string {
	if l.Network == "" {
		return "tcp"
	}
	return l.Network
}

// authenticate is the relay's Authenticate: verify, select, materialize.
// A refusal wraps codectypes.ErrLoginRefused; a verifier or source failure
// is wrapped too, since the client is refused either way, and logged with
// its cause, which never reaches the client.
func (a *laneAuth) authenticate(ctx context.Context, user, secret string) (session.Identity, Credential, error) {
	id, err := a.verifier.Verify(ctx, user, secret, a.peer)
	if err != nil {
		return a.refuse(session.Identity{Subject: user}, "verify", err)
	}
	if id.Attributes == nil {
		id.Attributes = map[string]string{}
	}
	id.Attributes[identityTypeAttribute] = a.identityType
	entry, err := a.selectEntry(ctx, id)
	if err != nil {
		return a.refuse(id, "select", err)
	}
	cred, err := a.sources[entry.Source].Credential(ctx, entry, id)
	if err != nil {
		return a.refuse(id, "credential", err)
	}
	cred.Name = entry.Name
	return id, cred, nil
}

func (a *laneAuth) refuse(id session.Identity, stage string, err error) (session.Identity, Credential, error) {
	if !errors.Is(err, codectypes.ErrLoginRefused) {
		// Not a refusal by rule: something this process owns failed. Say
		// why here, and give the client only that the login was refused.
		a.log.Warn("login refused by a failure, not a rule", "stage", stage, "principal", id.Principal(), "error", err)
		return id, Credential{}, fmt.Errorf("%w: %s failed", codectypes.ErrLoginRefused, stage)
	}
	return id, Credential{}, err
}

// authenticator is the libhoop form of authenticate, for a codec that
// terminates the login itself (ClickHouse).
func (a *laneAuth) authenticator() codectypes.Authenticator {
	return func(ctx context.Context, user, secret string) (codectypes.ClientIdentity, codectypes.UpstreamCredential, error) {
		id, cred, err := a.authenticate(ctx, user, secret)
		if err != nil {
			return codectypes.ClientIdentity{Subject: id.Subject}, codectypes.UpstreamCredential{}, err
		}
		return codectypes.ClientIdentity{Subject: id.Subject, Email: id.Email, Groups: id.Groups, Attributes: id.Attributes},
			codectypes.UpstreamCredential{User: cred.User, Password: cred.Password, Cert: cred.Cert, GSS: cred.GSS}, nil
	}
}

// passthrough is the relay's Passthrough: a startup user the lane relays
// unchanged.
func passthrough(users []string) func(user string) bool {
	if len(users) == 0 {
		return nil
	}
	return func(user string) bool { return slices.Contains(users, user) }
}

// selectEntry picks the pool entry for an identity: the first rule whose
// every condition holds, or the credential the lane's OPA names. No match
// is a refusal, not a default: a lane that fell back to its first entry
// would hand a credential to an identity nobody mapped.
func (a *laneAuth) selectEntry(ctx context.Context, id session.Identity) (CredentialEntry, error) {
	var name string
	switch {
	case a.opa != nil:
		picked, err := a.selectWithOPA(ctx, id)
		if err != nil {
			return CredentialEntry{}, err
		}
		name = picked
	default:
		for _, r := range a.rules {
			if ruleMatches(r, id, a.context) {
				name = r.Credential
				break
			}
		}
		if name == "" {
			return CredentialEntry{}, refused("no credential rule matches %s", id.Principal())
		}
	}
	for _, e := range a.pool {
		if e.Name == name {
			return e, nil
		}
	}
	return CredentialEntry{}, refused("credential %q is not in the pool", name)
}

// ruleMatches reports whether every condition of a rule holds for id. An
// empty When matches everyone.
func ruleMatches(r CredentialRule, id session.Identity, laneCtx map[string]string) bool {
	for key, want := range r.When {
		var got string
		switch {
		case key == "identity.subject":
			got = id.Subject
		case key == "identity.email":
			got = id.Email
		case key == "identity.groups":
			if !slices.Contains(id.Groups, want) {
				return false
			}
			continue
		case strings.HasPrefix(key, "identity.attributes."):
			got = id.Attributes[strings.TrimPrefix(key, "identity.attributes.")]
		case strings.HasPrefix(key, "context."):
			got = laneCtx[strings.TrimPrefix(key, "context.")]
		default:
			return false
		}
		if got != want {
			return false
		}
	}
	return true
}

// validateRuleKey refuses a When key the selector would never read.
func validateRuleKey(key string) bool {
	switch {
	case key == "identity.subject", key == "identity.email", key == "identity.groups":
		return true
	case strings.HasPrefix(key, "identity.attributes."):
		return len(key) > len("identity.attributes.")
	case strings.HasPrefix(key, "context."):
		return len(key) > len("context.")
	}
	return false
}

// selectWithOPA asks the lane's endpoint for the credential: a login
// statement with the identity in input.context, answered with `credential`
// beside `allow`. A denial refuses the login; an allow naming no credential
// refuses it too, since the policy was asked exactly that.
func (a *laneAuth) selectWithOPA(ctx context.Context, id session.Identity) (string, error) {
	names := make([]string, 0, len(a.pool))
	for _, e := range a.pool {
		names = append(names, e.Name)
	}
	pctx := map[string]string{
		"principal":  id.Principal(),
		"connection": a.lane,
	}
	if id.Subject != "" {
		pctx["subject"] = id.Subject
	}
	if id.Email != "" {
		pctx["email"] = id.Email
	}
	if len(id.Groups) > 0 {
		pctx["groups"] = strings.Join(id.Groups, ",")
	}
	for k, v := range id.Attributes {
		pctx[k] = v
	}
	for k, v := range a.context {
		pctx[k] = v
	}
	stmt := inspect.Statement{
		Protocol:  a.proto,
		Direction: inspect.FromClient,
		Operation: inspect.OpOther,
		Text:      "login",
		Metadata:  map[string]string{"phase": "login", "pool": strings.Join(names, ",")},
	}
	v := a.opa.EvaluateWith(stmt, &policy.EvalContext{Context: pctx, ConnCtx: ctx})
	switch {
	case v.Err != nil && v.Denied:
		return "", fmt.Errorf("credential selection: %w", v.Err)
	case v.Denied:
		return "", refused("%s", messageOr(v.Message, "denied by policy"))
	case v.Credential == "":
		return "", refused("the policy allowed the login and named no credential")
	}
	return v.Credential, nil
}

func messageOr(msg, fallback string) string {
	if msg == "" {
		return fallback
	}
	return msg
}

// credentialBoundaryRule is the guardrail every pooled SQL lane runs ahead
// of its own: under a swapped credential the database identity is the
// sidecar's choice, and a statement that switches it would hand the client
// a role nobody mapped. Anchored at the statement start, through leading
// comments, so a column named revert is not a match.
var credentialBoundaryRule = policy.Rule{
	Name:    "credential_boundary",
	Type:    policy.MatchPattern,
	Pattern: `(?is)^\s*(?:(?:--[^\n]*\n|/\*.*?\*/)\s*)*(?:set\s+(?:local\s+|session\s+)?role\b|reset\s+role\b|set\s+session\s+authorization\b|execute\s+as\b|revert\b)`,
	Message: "this listener logs in with a credential of its own; changing the session's role or authorization is not permitted",
}

// credentialBoundary prepends the boundary rule to a pooled lane's policy,
// outside any observe wrapper: the boundary enforces whatever the lane's
// mode. Only an SQL lane has a statement to match; the rest return pol.
func credentialBoundary(pol policy.Evaluator, proto inspect.Protocol) policy.Evaluator {
	switch proto {
	case inspect.Postgres, inspect.MySQL, inspect.MSSQL, inspect.Oracle, inspect.ClickHouse:
	default:
		return pol
	}
	rules, err := policy.NewRules([]policy.Rule{credentialBoundaryRule})
	if err != nil {
		// The pattern is a constant; a compile failure is a programmer
		// error, not a config one.
		panic("sidecar/daemon: credential boundary rule: " + err.Error())
	}
	if pol == nil {
		return rules
	}
	return policy.Chain{rules, pol}
}

// swap is the http codec's Swap: the request's Authorization, verified and
// exchanged for the upstream's. A minted token travels as a bearer, a
// user and password as basic.
func (a *laneAuth) swap(ctx context.Context, credential string) (codechttp.SwapResult, error) {
	user, secret := splitAuthorization(credential)
	id, cred, err := a.authenticate(ctx, user, secret)
	if err != nil {
		return codechttp.SwapResult{}, err
	}
	return codechttp.SwapResult{
		User:     cred.User,
		Password: cred.Password,
		Bearer:   cred.User == "" || a.entrySource(cred.Name) == SourceGCPImpersonation,
		Identity: id,
	}, nil
}

// upstreamAuthorization is the gRPC server's per-RPC hook: the header value
// the upstream sees for a credential swap already resolved.
func upstreamAuthorization(cred Credential, bearer bool) string {
	if bearer {
		return "Bearer " + cred.Password
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(cred.User+":"+cred.Password))
}

// entrySource names the source of a pool entry.
func (a *laneAuth) entrySource(name string) string {
	for _, e := range a.pool {
		if e.Name == name {
			return e.Source
		}
	}
	return ""
}

// requestIdentity is the gate's per-request resolver on a pooled http lane:
// the verifier alone, so a pooled connection rotates sessions when another
// caller's request arrives on it, and a refused caller is refused before
// anything is resolved for them.
func (a *laneAuth) requestIdentity() gate.RequestIdentity {
	return gate.RequestIdentityFunc(func(ctx context.Context, credential string) (session.Identity, error) {
		user, secret := splitAuthorization(credential)
		id, err := a.verifier.Verify(ctx, user, secret, a.peer)
		if err != nil {
			if !errors.Is(err, codectypes.ErrLoginRefused) {
				a.log.Warn("caller refused by a failure, not a rule", "error", err)
				return session.Identity{}, fmt.Errorf("%w: verification failed", codectypes.ErrLoginRefused)
			}
			return session.Identity{}, err
		}
		if id.Attributes == nil {
			id.Attributes = map[string]string{}
		}
		id.Attributes[identityTypeAttribute] = a.identityType
		return id, nil
	})
}

// splitAuthorization reads a credential lifted from Authorization: a bearer
// is the secret alone, basic is user and password, anything else is taken
// as the secret, so a verifier sees the key whichever way a client sent it.
func splitAuthorization(value string) (user, secret string) {
	value = strings.TrimSpace(value)
	scheme, rest, _ := strings.Cut(value, " ")
	rest = strings.TrimSpace(rest)
	switch {
	case strings.EqualFold(scheme, "Bearer"):
		return "", rest
	case strings.EqualFold(scheme, "Basic"):
		raw, err := base64.StdEncoding.DecodeString(rest)
		if err != nil {
			return "", ""
		}
		u, p, _ := strings.Cut(string(raw), ":")
		return u, p
	}
	return "", value
}
