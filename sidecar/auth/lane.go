package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/daemon"
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
	verifier     daemon.IdentitySource
	// scram is the SCRAM material lookup a hoop_credential lane offers a
	// driver; nil on every other verifier.
	scram   func(user string) (codectypes.SCRAMMaterial, bool)
	pool    []daemon.CredentialEntry
	sources map[string]daemon.CredentialSource
	rules   []daemon.CredentialRule
	// opa is the lane's decision endpoint when select.opa names the
	// credential; nil under rules.
	opa     *policy.OPAClient
	context map[string]string
	peer    daemon.PeerInfo
	log     *slog.Logger
}

// buildLaneAuth builds a lane's login termination. Every error is a config
// fact: an unlinked type, a source the pool names and the block does not
// configure, a selector with no endpoint. It logs through deps.Log, the
// lane's logger under the process logger Run installed.
func buildLaneAuth(lc daemon.ListenerConfig, name string, deps daemon.AuthDeps,
	set daemon.CredentialSet, sources map[string]daemon.CredentialSource) (daemon.LaneAuth, error) {
	if !lc.HasPool() {
		return nil, errors.New("the lane has no credentials pool")
	}
	typ := lc.IdentityType()
	build, ok := daemon.RegisteredIdentitySource(typ)
	if !ok {
		return nil, fmt.Errorf("identity type %q is not linked into this build", typ)
	}
	var idCfg daemon.IdentityConfig
	if lc.Identity != nil {
		idCfg = *lc.Identity
	}
	laneLog := deps.Log
	if laneLog == nil {
		laneLog = slog.Default().With("listener", name)
	}
	verifier, err := build(idCfg, daemon.IdentityDeps{Roots: deps.Roots, Log: laneLog, Lane: name, CredentialSet: set})
	if err != nil {
		return nil, fmt.Errorf("identity %s: %w", typ, err)
	}
	a := &laneAuth{
		lane:         name,
		proto:        inspect.Protocol(lc.Protocol),
		identityType: typ,
		verifier:     verifier,
		pool:         lc.Credentials.Pool,
		sources:      map[string]daemon.CredentialSource{},
		rules:        lc.Credentials.Select.Rules,
		context:      lc.Context,
		peer:         deps.Peer,
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
		if deps.OPA == nil {
			return nil, errors.New("credentials.select.opa needs an opa.url on this lane")
		}
		a.opa = deps.OPA
	}
	return a, nil
}

// Authenticate is the relay's Authenticate: verify, select, materialize.
// A refusal wraps codectypes.ErrLoginRefused; a verifier or source failure
// is wrapped too, since the client is refused either way, and logged with
// its cause, which never reaches the client.
func (a *laneAuth) Authenticate(ctx context.Context, user, secret string) (session.Identity, daemon.Credential, error) {
	id, err := a.verifier.Verify(ctx, user, secret, a.peer)
	if err != nil {
		return a.refuse(session.Identity{Subject: user}, "verify", err)
	}
	if id.Attributes == nil {
		id.Attributes = map[string]string{}
	}
	id.Attributes[daemon.IdentityTypeAttribute] = a.identityType
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

func (a *laneAuth) refuse(id session.Identity, stage string, err error) (session.Identity, daemon.Credential, error) {
	if !errors.Is(err, codectypes.ErrLoginRefused) {
		// Not a refusal by rule: something this process owns failed. Say
		// why here, and give the client only that the login was refused.
		a.log.Warn("login refused by a failure, not a rule", "stage", stage, "principal", id.Principal(), "error", err)
		return id, daemon.Credential{}, fmt.Errorf("%w: %s failed", codectypes.ErrLoginRefused, stage)
	}
	return id, daemon.Credential{}, err
}

// Authenticator is the libhoop form of Authenticate, for a codec that
// terminates the login itself (ClickHouse).
func (a *laneAuth) Authenticator() codectypes.Authenticator {
	return func(ctx context.Context, user, secret string) (codectypes.ClientIdentity, codectypes.UpstreamCredential, error) {
		id, cred, err := a.Authenticate(ctx, user, secret)
		if err != nil {
			return codectypes.ClientIdentity{Subject: id.Subject}, codectypes.UpstreamCredential{}, err
		}
		return codectypes.ClientIdentity{Subject: id.Subject, Email: id.Email, Groups: id.Groups, Attributes: id.Attributes},
			codectypes.UpstreamCredential{User: cred.User, Password: cred.Password, Cert: cred.Cert, GSS: cred.GSS}, nil
	}
}

// SCRAM is the material lookup of a hoop_credential lane; nil elsewhere.
func (a *laneAuth) SCRAM() func(user string) (codectypes.SCRAMMaterial, bool) { return a.scram }

func (a *laneAuth) IdentityType() string { return a.identityType }

func (a *laneAuth) PoolSize() int { return len(a.pool) }

// selectEntry picks the pool entry for an identity: the first rule whose
// every condition holds, or the credential the lane's OPA names. No match
// is a refusal, not a default: a lane that fell back to its first entry
// would hand a credential to an identity nobody mapped.
func (a *laneAuth) selectEntry(ctx context.Context, id session.Identity) (daemon.CredentialEntry, error) {
	var name string
	switch {
	case a.opa != nil:
		picked, err := a.selectWithOPA(ctx, id)
		if err != nil {
			return daemon.CredentialEntry{}, err
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
			return daemon.CredentialEntry{}, daemon.Refused("no credential rule matches %s", id.Principal())
		}
	}
	for _, e := range a.pool {
		if e.Name == name {
			return e, nil
		}
	}
	return daemon.CredentialEntry{}, daemon.Refused("credential %q is not in the pool", name)
}

// ruleMatches reports whether every condition of a rule holds for id. An
// empty When matches everyone.
func ruleMatches(r daemon.CredentialRule, id session.Identity, laneCtx map[string]string) bool {
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
		return "", daemon.Refused("%s", messageOr(v.Message, "denied by policy"))
	case v.Credential == "":
		return "", daemon.Refused("the policy allowed the login and named no credential")
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
		panic("sidecar/auth: credential boundary rule: " + err.Error())
	}
	if pol == nil {
		return rules
	}
	return policy.Chain{rules, pol}
}

// Swap is the http codec's Swap: the request's Authorization, verified and
// exchanged for the upstream's. A minted token travels as a bearer, a
// user and password as basic.
func (a *laneAuth) Swap(ctx context.Context, credential string) (codechttp.SwapResult, error) {
	user, secret := daemon.SplitAuthorization(credential)
	id, cred, err := a.Authenticate(ctx, user, secret)
	if err != nil {
		return codechttp.SwapResult{}, err
	}
	return codechttp.SwapResult{
		User:     cred.User,
		Password: cred.Password,
		Bearer:   a.bearer(cred),
		Identity: id,
	}, nil
}

// bearer reports whether a credential travels as a bearer token: one with
// no user, or a token minted for a service account.
func (a *laneAuth) bearer(cred daemon.Credential) bool {
	return cred.User == "" || a.entrySource(cred.Name) == daemon.SourceGCPImpersonation
}

// UpstreamAuthorization is the gRPC server's per-RPC hook: the header value
// the upstream sees for a credential swap already resolved.
func (a *laneAuth) UpstreamAuthorization(cred daemon.Credential) string {
	if a.bearer(cred) {
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

// RequestIdentity is the gate's per-request resolver on a pooled http lane:
// the verifier alone, so a pooled connection rotates sessions when another
// caller's request arrives on it, and a refused caller is refused before
// anything is resolved for them.
func (a *laneAuth) RequestIdentity() gate.RequestIdentity {
	return gate.RequestIdentityFunc(func(ctx context.Context, credential string) (session.Identity, error) {
		user, secret := daemon.SplitAuthorization(credential)
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
		id.Attributes[daemon.IdentityTypeAttribute] = a.identityType
		return id, nil
	})
}
