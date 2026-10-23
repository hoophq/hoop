package daemon

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/descriptors"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/session"
	telem "github.com/hoophq/hoop/sidecar/telemetry"
	codecgrpc "github.com/hoophq/libhoop/v2/codec/grpc"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

// A grpc lane is not a relay lane (ADR-0013). It terminates HTTP/2
// in-process, builds statements from parsed requests, and enters the gate at
// EvaluateStatement. libhoop owns the reusable HTTP/2 and protobuf mechanics;
// this package supplies the sidecar's identity, policy, audit and masking
// callbacks.

// GRPCCodecConfig controls what a grpc lane decodes and exposes to policy.
//
// The defaults expose nothing, matching HTTPCodecConfig: everything captured
// reaches the policy engine, the audit trail and, where an analyzer is
// configured, a third party.
type GRPCCodecConfig struct {
	// Descriptors names one or more serialized FileDescriptorSets
	// (protoc --include_imports --descriptor_set_out, buf build -o, or
	// -grpc-discover against an upstream that exposes server reflection) —
	// a single entry or a list; sets merge, byte-identical shared imports
	// dedupe, and conflicting copies of one file refuse at startup.
	// Multiple sets are the multi-team shape: each service's CI ships its
	// own artifact and no central re-bundle pipeline is required.
	//
	// An entry is a file path, or a URL a linked fetcher resolves at
	// startup: gs://BUCKET/OBJECT[?generation=N] with the descriptors/gcs
	// module. A bucket carries an artifact a ConfigMap cannot (1 MiB cap)
	// and lets each team publish without a redeploy of the sidecar's
	// volume. A scheme this binary does not link is refused at validation.
	//
	// Resolved ONCE, when the endpoint is built. The schema is bound into
	// the server the way the lane's callbacks are (ADR-0013), so a new
	// entry list is restart-bound drift (this block is in the reload
	// baseline) and a new version of an unpinned object is applied by a
	// restart, exactly like a new file at the same path. Pin
	// ?generation=N where two restarts must decode against one schema.
	//
	// Required for any payload work: schema-less protobuf walking loses
	// values as a function of their bytes, so no capture, masking or PII
	// scanning happens without it or Discover (ADR-0013).
	Descriptors DescriptorPaths `json:"descriptors,omitempty" label:"Descriptors" placeholder:"/etc/hoop-inspect/api.pb" help:"Protobuf descriptor sets, as paths on the sidecar host or gs:// URLs. Sets merge."`

	// Discover fetches the schema from the upstream over gRPC server
	// reflection when the lane starts, in place of Descriptors. See
	// GRPCDiscoverConfig.
	Discover *GRPCDiscoverConfig `json:"discover,omitempty" cap:"grpc_discover" label:"Discover the schema" help:"Fetches the schema from the upstream over gRPC server reflection when the listener starts, in place of descriptors." ui:"presence"`

	// Raw decodes without any schema: no descriptor set, no reflection.
	// See GRPCRawConfig for what that can and cannot do.
	Raw *GRPCRawConfig `json:"raw,omitempty" cap:"grpc_raw" label:"Decode without a schema" help:"Renders messages by field number for audit, PII rules and AI analysis. Best effort: a value can be misread. Masks only the string fields the config file declares." ui:"presence"`

	// CapturePayload renders decoded request and response messages into
	// per-message Statements so payload-matching rules (pii, pattern_match)
	// and OPA can read them. Requires Descriptors.
	CapturePayload bool `json:"capture_payload,omitempty" label:"Capture the payload" help:"Needed for masking, PII and AI analysis on this listener."`

	// MaxPayloadBytes truncates a captured rendering. Zero uses the lane
	// default. Masking does not read this: it rewrites decoded fields
	// whatever their size.
	MaxPayloadBytes int `json:"max_payload_bytes,omitempty" label:"Max payload bytes" help:"0 uses the lane default."`

	// Strict refuses an RPC whose payload cannot be read: a method the
	// descriptor set does not define is refused before the upstream is
	// dialed (FAILED_PRECONDITION), and a message that does not decode as
	// its declared type ends the RPC (INTERNAL). Off, the default, such
	// payloads are forwarded with method-level inspection only, and each
	// degradation is logged. A lane with mask rules fails closed either
	// way: a redactor must not forward what it cannot decode.
	Strict bool `json:"strict,omitempty" label:"Strict decoding" help:"Refuses an RPC whose payload the descriptors cannot read."`

	// Metadata names the request metadata headers to expose to policy,
	// matched case-insensitively. There is no capture-all, and the same
	// headers HTTPCodecConfig refuses are refused here; authorization is
	// where a gRPC bearer token lives.
	Metadata []string `json:"metadata,omitempty" label:"Metadata" help:"Allowlist exposed to policy. The same four headers HTTP refuses are refused here."`
}

// GRPCRawConfig is a grpc lane with no schema at all.
//
// Messages render as JSON keyed by field number, {"1":"INV-1001"}, so the
// audit trail, pii and pattern_match rules and an analyzer see the values
// without their names. The wire does not say whether a length-delimited
// value is a string, bytes or a nested message, so the rendering guesses,
// and a wrong guess can hide a value from a detector: best effort, not the
// enforcement a descriptor-backed lane gives.
//
// Masking rewrites only StringFields: the operator states which response
// paths are strings, the type the wire lacks. Mask rules match those paths
// by field number (columns: ["2"]) or by detector, and nothing else is
// ever rewritten. Bytes that contradict a declaration refuse the message.
type GRPCRawConfig struct {
	// StringFields declares, per method, the response field paths that are
	// strings. Edited in the config file: the form has no list of objects.
	StringFields []GRPCRawStringFields `json:"string_fields,omitempty" ui:"-"`
}

// GRPCRawStringFields is one method's declared string paths.
type GRPCRawStringFields struct {
	// Method is the canonical path, /demo.v1.Ledger/GetInvoice.
	Method string `json:"method,omitempty"`
	// Response lists field-number paths: "2", or "4.1" for field 1 of the
	// message in field 4.
	Response []string `json:"response,omitempty"`
}

// fields is the declarations in libhoop's shape. A method listed twice is
// reported, not merged: two entries for one method is a config mistake.
func (r *GRPCRawConfig) fields() (codecgrpc.RawFields, []string) {
	out := codecgrpc.RawFields{}
	var dups []string
	for _, sf := range r.StringFields {
		if _, seen := out[sf.Method]; seen {
			dups = append(dups, sf.Method)
			continue
		}
		out[sf.Method] = codecgrpc.RawMethod{Response: sf.Response}
	}
	return out, dups
}

// grpcDiscoverFromReflection is the only schema source discovery knows.
const grpcDiscoverFromReflection = "reflection"

// GRPCDiscoverConfig makes a grpc lane fetch its schema from the upstream
// it fronts, the -grpc-discover bootstrap run at every start.
//
// The schema then comes from the service being policed, so a deploy can
// rename a field out from under a mask rule or leave one out of what it
// reports. SHA256 closes that: the lane refuses a schema it was not told
// to expect.
//
// While discovery fails the lane still listens. It refuses every RPC with
// UNAVAILABLE, or with FailOpen forwards them with method-level inspection
// only, and retries in the background until a schema loads.
type GRPCDiscoverConfig struct {
	// From names the schema source. "reflection", the only one, is also
	// what empty means, so `discover: {}` turns discovery on.
	From string `json:"from,omitempty" label:"Source" enum:"reflection" default:"reflection" help:"Where the schema comes from. The upstream must expose gRPC server reflection."`

	// SHA256 pins the discovered schema: the hex digest -grpc-discover
	// prints. A schema with another digest is a discovery failure. Empty
	// accepts whatever the upstream reports.
	SHA256 string `json:"sha256,omitempty" label:"Pinned SHA-256" placeholder:"output of -grpc-discover" help:"Refuses a schema with another digest, so the service cannot rename a field out from under a mask rule. Empty accepts any schema."`

	// FailOpen forwards RPCs while discovery fails, with method-level
	// inspection only. Default false: the lane refuses them, because a
	// lane that cannot decode a message cannot audit or scan it.
	FailOpen bool `json:"fail_open,omitempty" label:"Fail open" help:"Forwards RPCs while the schema is unavailable, auditing the method and status only. Off refuses them."`
}

func (d *GRPCDiscoverConfig) validate(lane string) []string {
	var problems []string
	if d.From != "" && d.From != grpcDiscoverFromReflection {
		problems = append(problems, fmt.Sprintf(
			"listener %q: grpc.discover.from is %q; the only source is %q",
			lane, d.From, grpcDiscoverFromReflection))
	}
	if d.SHA256 != "" && !isSHA256Hex(d.SHA256) {
		problems = append(problems, fmt.Sprintf(
			"listener %q: grpc.discover.sha256 is not a hex SHA-256 digest; copy it from -grpc-discover",
			lane))
	}
	return problems
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func (g *GRPCCodecConfig) validate(lane string, onHost bool) []string {
	if g == nil {
		return nil
	}
	var problems []string
	for _, name := range g.Metadata {
		lower := strings.ToLower(strings.TrimSpace(name))
		for _, bad := range forbiddenHeaders {
			if lower == bad {
				problems = append(problems, fmt.Sprintf(
					"listener %q: metadata header %q may not be exposed to policy", lane, name))
			}
		}
	}
	if g.MaxPayloadBytes < 0 {
		problems = append(problems, fmt.Sprintf(
			"listener %q: grpc.max_payload_bytes is negative", lane))
	}
	for _, p := range g.Descriptors {
		if strings.TrimSpace(p) == "" {
			problems = append(problems, fmt.Sprintf(
				"listener %q: grpc.descriptors contains an empty path", lane))
			continue
		}
		// Decided here, not at the fetch: a scheme nobody linked must be a
		// validate failure naming the module, not a startup "no such file"
		// for a path that starts with gs://. Only the sidecar binary knows
		// what it links.
		if scheme := descriptors.Scheme(p); scheme != "" && onHost && !descriptors.Linked(scheme) {
			problems = append(problems, fmt.Sprintf(
				"listener %q: grpc.descriptors %q uses scheme %q, which this binary does not "+
					"link (%s)", lane, p, scheme, describeLinkedFetchers()))
		}
	}
	if g.Discover != nil {
		problems = append(problems, g.Discover.validate(lane)...)
		if len(g.Descriptors) > 0 {
			problems = append(problems, fmt.Sprintf(
				"listener %q: grpc.discover and grpc.descriptors both name the schema; keep one", lane))
		}
		if g.Discover.FailOpen && g.Strict {
			problems = append(problems, fmt.Sprintf(
				"listener %q: grpc.discover.fail_open forwards RPCs the lane cannot decode and "+
					"grpc.strict refuses them; keep one", lane))
		}
	}
	if g.Raw != nil {
		if len(g.Descriptors) > 0 || g.Discover != nil {
			problems = append(problems, fmt.Sprintf(
				"listener %q: grpc.raw decodes without a schema, and grpc.descriptors or "+
					"grpc.discover names one; keep one", lane))
		}
		if g.Strict {
			problems = append(problems, fmt.Sprintf(
				"listener %q: grpc.strict needs a schema, and grpc.raw has none", lane))
		}
		fields, dups := g.Raw.fields()
		for _, m := range dups {
			problems = append(problems, fmt.Sprintf(
				"listener %q: grpc.raw.string_fields lists %q twice", lane, m))
		}
		if err := fields.Validate(); err != nil {
			problems = append(problems, fmt.Sprintf("listener %q: %v", lane, err))
		}
	}
	if g.Strict && !g.hasSchema() {
		problems = append(problems, fmt.Sprintf(
			"listener %q: grpc.strict needs grpc.descriptors or grpc.discover; strictness about "+
				"payloads nothing can decode would refuse every RPC", lane))
	}
	if g.CapturePayload && !g.hasSchema() {
		// Without a schema the lane cannot decode a message soundly, so a
		// capture flag would produce nothing and every rule reading payloads
		// would silently never fire: the failure this package refuses
		// everywhere else.
		problems = append(problems, fmt.Sprintf(
			"listener %q: grpc.capture_payload needs grpc.descriptors, grpc.discover or grpc.raw; without a "+
				"descriptor set the lane cannot decode a message", lane))
	}
	return problems
}

// DescriptorPaths accepts one path or a list in the config file, so the
// single-set spelling every existing config uses keeps working while a
// multi-team lane lists one artifact per service.
type DescriptorPaths []string

// UnmarshalJSON accepts "path" and ["a", "b"].
func (d *DescriptorPaths) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s == "" {
			*d = nil
			return nil
		}
		*d = DescriptorPaths{s}
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	*d = DescriptorPaths(list)
	return nil
}

// MarshalJSON keeps the single-path spelling stable for /config readers.
func (d DescriptorPaths) MarshalJSON() ([]byte, error) {
	if len(d) == 1 {
		return json.Marshal(d[0])
	}
	return json.Marshal([]string(d))
}

// hasSchema reports whether the lane decodes payloads: from pinned sets, a
// discovered one, or raw without one. Tolerates a nil receiver so call
// sites read as one condition.
func (g *GRPCCodecConfig) hasSchema() bool {
	return g != nil && (len(g.Descriptors) > 0 || g.Discover != nil || g.Raw != nil)
}

// GRPCServer is the running side of a grpc lane. Serve blocks until the
// context ends or the listener fails; Close is idempotent.
type GRPCServer interface {
	Serve(ctx context.Context) error
	Close() error
	Addr() net.Addr
	Stats() (active, total, denied int64)
	// Notes returns descriptor-derived method and maskable-path summaries
	// for -validate. It must not expose message values.
	Notes() []string
}

// isGRPC reports whether a listener is a grpc lane.
func isGRPC(lc ListenerConfig) bool {
	return inspect.Protocol(lc.Protocol) == inspect.GRPC
}

// isSpanner reports whether a listener is a spanner lane: GoogleSQL
// extracted from Cloud Spanner RPCs, carried over the same gRPC transport.
func isSpanner(lc ListenerConfig) bool {
	return inspect.Protocol(lc.Protocol) == inspect.Spanner
}

// isGRPCTransport reports whether a listener terminates HTTP/2 in-process
// the way ADR-0013 describes. Every transport fact — the grpc block, the
// downstream_tls carve-out, descriptor-gated capture and masking, the
// server construction branch, -grpc-discover — keys on this, while
// statement SEMANTICS (which Protocol the statements carry, whether SQL is
// extracted) key on the specific predicate above. Keeping the two apart is
// what lets spanner ride the grpc machinery without becoming "grpc with a
// flag": a new transport sharer extends this function and nothing else.
func isGRPCTransport(lc ListenerConfig) bool {
	return isGRPC(lc) || isSpanner(lc)
}

const (
	grpcPermissionDenied = 7
	grpcUnavailable      = 14
	grpcUnauthenticated  = 16
)

// metadataGRPCSchema is set to "unavailable" on every statement of an RPC a
// discovering lane forwarded before its schema loaded.
const metadataGRPCSchema = "grpc.schema"

// grpcMetadataAllowlist is the lower-cased metadata a grpc lane exposes to
// policy and audit. A lane that holds adds analyzer.ClientModeHeaders, the same
// opt-in as an http lane. The analyzer block swaps on a reload, so the lane
// builds both lists and picks one per RPC from its live rules.
func grpcMetadataAllowlist(gc GRPCCodecConfig, holds bool) []string {
	out := make([]string, 0, len(gc.Metadata)+2)
	for _, name := range gc.Metadata {
		out = append(out, strings.ToLower(strings.TrimSpace(name)))
	}
	if holds {
		out = append(out, analyzer.ClientModeHeaders()...)
	}
	return out
}

// buildGRPCServer resolves one grpc lane's transport facts and builds the
// reusable libhoop endpoint with sidecar-owned RPC callbacks. The returned
// liveRules is the lane's reload target: every RPC reads its rules there.
//
// settle reports the upstream's answer to a statement a reserved approval
// released; nil in a process with no control plane.
func buildGRPCServer(
	ln lane,
	ac AuditConfig,
	sink audit.Sink,
	settle gate.SettleFunc,
	log *slog.Logger,
) (GRPCServer, *liveRules, error) {
	lc := ln.cfg
	var gc GRPCCodecConfig
	if lc.GRPC != nil {
		gc = *lc.GRPC
	}

	laneLog := log.With("listener", ln.name)
	metadataPlain := grpcMetadataAllowlist(gc, false)
	metadataHeld := grpcMetadataAllowlist(gc, true)
	rules := newLiveRules(ln)
	failOnAuditError := ac.failOnAuditError()

	// libhoop convention: configuration travels as a map[string]string the
	// server validates (unknown keys refused). TLS rides as file paths, so
	// the server loads the material and a bad path fails before binding.
	opts := map[string]string{
		"name":     ln.name,
		"listen":   lc.Listen,
		"upstream": lc.Upstream,
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
	// Payload settings need a schema, so a discovering lane's stand-in
	// server leaves them out until one loads.
	payloadOpts := map[string]string{}
	if gc.CapturePayload {
		payloadOpts["capture_payload"] = "true"
	}
	if gc.MaxPayloadBytes > 0 {
		payloadOpts["max_payload_bytes"] = strconv.Itoa(gc.MaxPayloadBytes)
	}
	if gc.Strict {
		payloadOpts["strict"] = "true"
	}
	if ln.masker != nil {
		payloadOpts["mask_responses"] = "true"
	}
	if ut := lc.UpstreamTLS; ut != nil {
		opts["upstream_tls"] = "true"
		if ut.CAFile != "" {
			opts["upstream_tls_ca_file"] = ut.CAFile
		}
		if ut.ServerName != "" {
			opts["upstream_tls_server_name"] = ut.ServerName
		}
		if ut.CertFile != "" {
			opts["upstream_tls_cert_file"] = ut.CertFile
		}
		if ut.KeyFile != "" {
			opts["upstream_tls_key_file"] = ut.KeyFile
		}
		if ut.InsecureSkipVerify {
			opts["upstream_tls_insecure_skip_verify"] = "true"
			log.Warn("upstream certificate verification is DISABLED",
				"listener", ln.name, "upstream", lc.Upstream)
		}
	}
	if dt := lc.DownstreamTLS; dt != nil {
		opts["downstream_tls_cert_file"] = dt.CertFile
		opts["downstream_tls_key_file"] = dt.KeyFile
	}

	// The lane's statement protocol. A spanner lane shares every transport
	// fact above — same libhoop server, same descriptor plumbing — but its
	// statements carry inspect.Spanner so policy, audit, dialect selection
	// (AnalyzeSQL) and the analyzer's content builder all see the lane for
	// what it fronts, not for how it travels.
	proto := inspect.GRPC
	if isSpanner(lc) {
		proto = inspect.Spanner
	}

	// openFor builds the per-RPC callback. gap is nil on a lane that holds
	// its schema, and set on a discovering lane's stand-in.
	openFor := func(gap *grpcSchemaGap) codecgrpc.OpenFunc {
		return func(ctx context.Context, info codecgrpc.RPCInfo) (*codecgrpc.RPCHandler, *codecgrpc.Status, error) {
			identity := session.Identity{PeerAddr: info.Request.RemoteAddr}
			if lc.IdentityHeader != "" {
				identity.Subject = info.Request.Header.Get(lc.IdentityHeader)
			}
			if identity.Subject == "" {
				identity.Subject = grpcPeerSubject(info.Request)
			}
			// A pooled lane verifies the RPC's bearer and swaps it for the
			// upstream credential. A refusal still opens the session, so
			// the trail records it under the claimed identity, and ends
			// it with the handler's Close.
			var cred Credential
			var loginErr error
			if ln.auth != nil {
				user, secret := splitAuthorization(info.Request.Header.Get("Authorization"))
				var verified session.Identity
				verified, cred, loginErr = ln.auth.authenticate(ctx, user, secret)
				if loginErr == nil {
					verified.PeerAddr = identity.PeerAddr
					identity = verified
				} else if user != "" {
					identity.Subject = user
				}
			}

			sess := session.New(proto, identity)
			sess.Connection = ln.name
			sess.Upstream = lc.Upstream
			if ln.auth != nil && loginErr == nil {
				sess.Metadata = map[string]string{
					"credential":    cred.Name,
					"upstream_user": cred.User,
					"identity_type": ln.auth.identityType,
					"verified":      "true",
				}
			}
			// Acquired once per RPC: the gate keeps this generation for the RPC's
			// whole life, and the next RPC reads what a reload swapped in. The
			// release runs when the gate closes, which is what tells a reload
			// the outgoing generation's analyzers are no longer reachable.
			live, release := rules.acquire()
			allowedMetadata := metadataPlain
			if live.holds {
				allowedMetadata = metadataHeld
			}
			// The RPC span encloses the request headers, every message and
			// the trailers; the trace is sampled here, once per call.
			var spanCtx context.Context
			var endExchange telem.EndExchange
			if ln.telemetry != nil {
				spanCtx, endExchange = ln.telemetry.BeginExchange(ctx, telem.ExchangeRPC,
					telem.Attr{Key: "rpc.method", Value: info.Request.URL.Path},
					telem.Attr{Key: "hoop.session_id", Value: string(sess.ID)},
					telem.Attr{Key: "hoop.principal", Value: sess.Identity.Principal()},
				)
				ctx = spanCtx
			}
			g, err := gate.NewStatementGate(sess, gate.Config{
				Protocol:         proto,
				Policy:           live.policy,
				Audit:            sink,
				Masker:           live.masker,
				FailOnAuditError: failOnAuditError,
				Metrics:          ln.metrics,
				Telemetry:        ln.telemetry,
				Settle:           settle,
			})
			if err != nil {
				// No handler, so libhoop calls no Close: release here.
				release()
				if endExchange != nil {
					endExchange(err)
				}
				return nil, nil, err
			}

			state := &grpcRPCState{
				gate:        g,
				stmts:       newLaneStatements(info.Request, info.Service, info.Method, allowedMetadata, proto, lc.Spanner),
				log:         laneLog,
				release:     release,
				telemetry:   ln.telemetry,
				spanCtx:     spanCtx,
				endExchange: endExchange,
			}
			switch {
			case gap != nil:
				state.stmts.schema = "unavailable"
			case gc.Raw != nil:
				state.stmts.schema = "raw"
			}
			handler := state.callbacks()
			if ln.auth != nil && loginErr == nil {
				bearer := cred.User == "" || ln.auth.entrySource(cred.Name) == SourceGCPImpersonation
				value := upstreamAuthorization(cred, bearer)
				handler.UpstreamAuthorization = func(*http.Request) (string, bool) { return value, true }
			}
			if err := g.Start(ctx); err != nil {
				if failOnAuditError {
					return handler, &codecgrpc.Status{
						Code:    13,
						Message: "audit trail unavailable; RPC refused",
					}, nil
				}
				laneLog.Warn("grpc session start not recorded", "error", err)
			}
			if loginErr != nil {
				msg := "login refused"
				if errors.Is(loginErr, codectypes.ErrLoginRefused) {
					msg = loginErr.Error()
				}
				g.RefuseLogin(ctx, identity.Subject, msg)
				return handler, &codecgrpc.Status{Code: grpcUnauthenticated, Message: msg}, nil
			}

			if gap != nil && gap.refuse {
				reason := "schema discovery failed: " + gap.reason()
				if err := g.RecordActivity(ctx, "rpc_refused", map[string]string{
					"method": info.Request.URL.Path,
					"reason": reason,
				}); err != nil {
					laneLog.Warn("grpc refusal not recorded", "error", err)
				}
				return handler, &codecgrpc.Status{Code: grpcUnavailable, Message: reason}, nil
			}

			d := g.EvaluateStatement(ctx, state.stmts.request(info.Request))
			state.logDecisionError("request headers", d)
			if !d.Allowed {
				return handler, grpcDeniedStatus(d), nil
			}
			return handler, nil, nil
		}
	}

	if gc.Raw != nil {
		// No schema to load: the lane renders by field number and masks
		// only the declared paths. validate refused a duplicate method.
		fields, _ := gc.Raw.fields()
		maps.Copy(opts, payloadOpts)
		srv, err := codecgrpc.NewServerRaw(opts, fields, openFor(nil), laneLog)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", ln.name, err)
		}
		return srv, rules, nil
	}

	if gc.Discover != nil {
		// Built here, not at the first attempt, so a bad path fails
		// -validate. Discovery dials with the lane's own upstream_tls,
		// the way -grpc-discover does.
		tlsConf, err := lc.UpstreamTLS.BuildTLS(nil)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: upstream_tls: %w", ln.name, err)
		}
		d := &discoveringGRPCServer{
			name:     ln.name,
			upstream: lc.Upstream,
			cfg:      *gc.Discover,
			log:      laneLog,
			fetch: func(ctx context.Context) ([]byte, error) {
				return codecgrpc.Discover(ctx, lc.Upstream, tlsConf, laneLog)
			},
		}
		gap := &grpcSchemaGap{refuse: !gc.Discover.FailOpen, reason: d.lastFailure}
		d.build = func(schema *codecgrpc.Schema, listen string) (*codecgrpc.Server, error) {
			o := maps.Clone(opts)
			if listen != "" {
				o["listen"] = listen
			}
			if schema == nil {
				return codecgrpc.NewServerWithSchema(o, nil, openFor(gap), laneLog)
			}
			maps.Copy(o, payloadOpts)
			return codecgrpc.NewServerWithSchema(o, schema, openFor(nil), laneLog)
		}
		// The stand-in takes every setting but the payload ones; building
		// one now refuses a bad TLS file before the lane is reported built.
		if _, err := d.build(nil, ""); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", ln.name, err)
		}
		return d, rules, nil
	}

	// Descriptor sets do not travel as a setting: an entry may be a URL, and
	// resolving it — a fetch with credentials — is this package's job, not
	// the codec's. The merged schema is handed over already loaded.
	schema, err := loadGRPCSchema(gc.Descriptors, ln.trustRoots)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ln.name, err)
	}
	maps.Copy(opts, payloadOpts)
	srv, err := codecgrpc.NewServerWithSchema(opts, schema, openFor(nil), laneLog)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ln.name, err)
	}
	return srv, rules, nil
}

// grpcSchemaGap is how a discovering lane's stand-in treats an RPC while it
// has no schema: refuse it, or forward it with method-level inspection.
type grpcSchemaGap struct {
	refuse bool
	// reason is the latest discovery failure, read per RPC.
	reason func() string
}

// grpcDescriptorFetchTimeout bounds one remote descriptor fetch. Generous,
// because the artifact this feature exists for is the one too large for a
// ConfigMap, and a stall here is a startup that never completes rather than
// a request that fails, so the operator sees it either way.
const grpcDescriptorFetchTimeout = 2 * time.Minute

// loadGRPCSchema resolves every descriptors entry — files read here, URLs
// fetched through the descriptors registry — and merges them into one
// schema under libhoop's rules. Nil for a lane with no descriptors.
//
// Local files are read here rather than by passing paths to libhoop so one
// merge sees every set: conflict detection between a file and a bucket
// artifact needs both in the same call.
//
// roots is the process trust pool. It reaches the object-store fetch and its
// credential exchange, not the lane's upstream: that TLS is libhoop's.
func loadGRPCSchema(entries DescriptorPaths, roots *x509.CertPool) (*codecgrpc.Schema, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	sources := make([]codecgrpc.SchemaSource, 0, len(entries))
	client := outboundHTTPClient(roots, "descriptors")
	for _, entry := range entries {
		var blob []byte
		var err error
		if descriptors.Scheme(entry) != "" {
			ctx, cancel := context.WithTimeout(context.Background(), grpcDescriptorFetchTimeout)
			blob, err = descriptors.Fetch(ctx, entry, client)
			cancel()
		} else {
			blob, err = os.ReadFile(entry)
		}
		if err != nil {
			return nil, fmt.Errorf("grpc descriptors: %w", err)
		}
		sources = append(sources, codecgrpc.SchemaSource{Name: entry, Blob: blob})
	}
	return codecgrpc.LoadSchemaSources(sources)
}

func describeLinkedFetchers() string {
	linked := descriptors.Registered()
	if len(linked) == 0 {
		return "no descriptor fetcher is linked; build github.com/hoophq/hoop/sidecar/cmd"
	}
	return "linked: " + strings.Join(linked, ", ")
}

type grpcRPCState struct {
	gate  *gate.Gate
	stmts *laneStatements
	log   *slog.Logger
	// release returns the RPC's rule generation to its liveRules. libhoop
	// calls Close for every RPC that got a handler, refused or not.
	release func()

	// telemetry is the lane's recorder, nil without a telemetry block.
	// spanCtx carries the RPC's exchange span and endExchange closes it;
	// libhoop hands each callback its own request context, so the span is
	// merged back in by evalCtx rather than threaded through libhoop.
	telemetry   telem.LaneRecorder
	spanCtx     context.Context
	endExchange telem.EndExchange

	// requestAt is when the FIRST request message libhoop forwarded was
	// judged, as UnixNano, and answered says whether the first response
	// has been timed against it. One gate per RPC makes the pairing exact
	// for a unary call; on a streaming call the measurement is from the
	// first forwarded request to the first response, which is the one
	// pairing the protocol defines. A denied message is never forwarded,
	// so it leaves no stamp. Atomics, because libhoop judges request
	// messages on the transport's body writer and responses on the handler
	// goroutine, and a bidirectional call has both running at once.
	requestAt atomic.Int64
	answered  atomic.Bool
}

// evalCtx is the context a statement is judged under: the callback's own,
// for its deadline and cancellation, with the RPC span's values layered on
// top so the statement span nests under the RPC.
func (r *grpcRPCState) evalCtx(ctx context.Context) context.Context {
	if r.spanCtx == nil {
		return ctx
	}
	return spanValuesCtx{Context: ctx, span: r.spanCtx}
}

// spanValuesCtx reads values from span first, everything else from the
// embedded context. The stdlib merges nothing across two contexts, and the
// span is the only value the exchange needs to lend.
type spanValuesCtx struct {
	context.Context
	span context.Context
}

func (c spanValuesCtx) Value(key any) any {
	if v := c.span.Value(key); v != nil {
		return v
	}
	return c.Context.Value(key)
}

// requestSent stamps the request for the upstream measurement; firstAnswer
// records it when the first response arrives.
func (r *grpcRPCState) requestSent() {
	if r.telemetry != nil {
		r.requestAt.CompareAndSwap(0, time.Now().UnixNano())
	}
}

func (r *grpcRPCState) firstAnswer(ctx context.Context) {
	if r.telemetry == nil {
		return
	}
	sent := r.requestAt.Load()
	if sent == 0 || !r.answered.CompareAndSwap(false, true) {
		return
	}
	r.telemetry.Upstream(ctx, time.Since(time.Unix(0, sent)), telem.PairingExact)
}

func (r *grpcRPCState) callbacks() *codecgrpc.RPCHandler {
	return &codecgrpc.RPCHandler{
		RequestMessage:  r.requestMessage,
		ResponseMessage: r.responseMessage,
		ResponseStatus:  r.responseStatus,
		MaskCell:        r.maskCell,
		RecordMasked:    r.recordMasked,
		Close:           r.close,
	}
}

func (r *grpcRPCState) requestMessage(
	cbCtx context.Context,
	rendered string,
	truncated bool,
	index int,
) *codecgrpc.Status {
	ctx := r.evalCtx(cbCtx)
	status := r.judgeRequestMessage(ctx, rendered, truncated, index)
	if status == nil {
		// Stamped only when the message is forwarded: a denied one never
		// leaves, so the upstream's answer cannot be measured from it.
		r.requestSent()
	}
	return status
}

func (r *grpcRPCState) judgeRequestMessage(ctx context.Context, rendered string, truncated bool, index int) *codecgrpc.Status {
	// A spanner lane reads THROUGH the message when it can: the SQL inside
	// an ExecuteSql or a DDL batch is what operation and table rules were
	// written against, and the protojson wrapper around it matches nothing.
	// Each extracted statement is evaluated on its own and the first denial
	// wins, so one DELETE inside an otherwise harmless ExecuteBatchDml
	// refuses the whole message — the batch is atomic upstream, so letting
	// the rest through would be a partial evaluation of an all-or-nothing
	// request. A SQL-bearing method whose rendering yielded NO SQL is the
	// fail-closed branch: an OpUnknown statement, never the generic OpCall
	// one, or truncating the capture would be a policy bypass. Methods
	// carrying no SQL, and lanes other than spanner, keep the generic
	// per-message statement below.
	if r.stmts.protocol == inspect.Spanner {
		req, sqlBearing := spannerSQLStatements(r.stmts.service, r.stmts.method, rendered)
		if sqlBearing && len(req.sqls) > 0 {
			// The dialect is decided once per message, from the database
			// the request names and the lane's config — never from the
			// SQL text, which the client controls. Under per_database an
			// unlisted database is the same fail-closed verdict as an
			// unreadable rendering: a rule naming `unknown` refuses it.
			dialect, d, ok := r.stmts.spanner.dialectFor(req.database, req.declared)
			if !ok {
				return r.evaluateSpannerUnreadable(ctx, rendered, truncated, index,
					fmt.Sprintf("spanner.dialect is per_database and database %q is not listed", req.database))
			}
			for i, sql := range req.sqls {
				dec := r.gate.EvaluateStatement(ctx,
					r.stmts.spannerSQL(sql, rendered, truncated, index, i+1, req.database, dialect, d))
				r.logDecisionError("request message", dec)
				if !dec.Allowed {
					return grpcDeniedStatus(dec)
				}
			}
			return nil
		}
		if sqlBearing {
			return r.evaluateSpannerUnreadable(ctx, rendered, truncated, index, "")
		}
	}
	d := r.gate.EvaluateStatement(ctx,
		r.stmts.message(inspect.FromClient, rendered, truncated, index))
	r.logDecisionError("request message", d)
	if !d.Allowed {
		return grpcDeniedStatus(d)
	}
	return nil
}

// evaluateSpannerUnreadable runs the fail-closed statement for a SQL-bearing
// message whose SQL could not be read. reason "" lets the statement pick
// the rendering's own explanation.
func (r *grpcRPCState) evaluateSpannerUnreadable(ctx context.Context, rendered string, truncated bool, index int, reason string) *codecgrpc.Status {
	d := r.gate.EvaluateStatement(ctx, r.stmts.spannerUnreadable(rendered, truncated, index, reason))
	r.logDecisionError("request message", d)
	if !d.Allowed {
		return grpcDeniedStatus(d)
	}
	return nil
}

func (r *grpcRPCState) responseMessage(
	cbCtx context.Context,
	rendered string,
	truncated bool,
	index int,
) *codecgrpc.Status {
	ctx := r.evalCtx(cbCtx)
	r.firstAnswer(ctx)
	d := r.gate.EvaluateStatement(ctx,
		r.stmts.message(inspect.FromServer, rendered, truncated, index))
	r.logDecisionError("response message", d)
	if !d.Allowed {
		return grpcDeniedStatus(d)
	}
	return nil
}

func (r *grpcRPCState) responseStatus(
	cbCtx context.Context,
	trailers http.Header,
	code int,
	message string,
) *codecgrpc.Status {
	ctx := r.evalCtx(context.WithoutCancel(cbCtx))
	// A unary error answers with trailers alone, so the status is the
	// first answer when no message preceded it.
	r.firstAnswer(ctx)
	d := r.gate.EvaluateStatement(ctx, r.stmts.trailer(trailers, code, message))
	r.logDecisionError("response trailers", d)
	if !d.Allowed {
		return grpcDeniedStatus(d)
	}
	return nil
}

func (r *grpcRPCState) maskCell(path string, value []byte) ([]byte, []string, int) {
	if r.gate.Masker() == nil {
		return value, nil, 0
	}
	return r.gate.Masker().MaskCell(path, value)
}

func (r *grpcRPCState) recordMasked(ctx context.Context, entities []string, count int) error {
	return r.gate.RecordMasked(ctx, entities, count)
}

func (r *grpcRPCState) close(ctx context.Context) error {
	err := r.gate.Close(r.evalCtx(context.WithoutCancel(ctx)))
	// After Close: the gate's last statement has been evaluated.
	r.release()
	if r.endExchange != nil {
		r.endExchange(err)
	}
	if err != nil {
		r.log.Warn("grpc session end not recorded", "error", err)
	}
	return err
}

func (r *grpcRPCState) logDecisionError(phase string, d gate.Decision) {
	if d.Err != nil {
		r.log.Warn("grpc statement evaluation continued after error",
			"phase", phase, "error", d.Err)
	}
}

// grpcDeniedStatus is PERMISSION_DENIED with the operator's message. The
// trailers are the markers an http lane sends as headers, so a client finds a
// review without parsing grpc-message.
func grpcDeniedStatus(d gate.Decision) *codecgrpc.Status {
	trailers := http.Header{"X-Hoop-Denied": {"policy"}}
	if d.Review != nil {
		trailers = proxy.ReviewHeaders(*d.Review)
	}
	return &codecgrpc.Status{Code: grpcPermissionDenied, Message: d.Message, Trailers: trailers}
}

// grpcPeerSubject names the caller from a verified client certificate, the
// fallback when no authenticating proxy supplies an identity header. SPIFFE
// deployments put the workload identity in the URI SAN; plain mTLS uses the
// subject common name.
func grpcPeerSubject(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	cert := r.TLS.PeerCertificates[0]
	if len(cert.URIs) > 0 {
		return cert.URIs[0].String()
	}
	return cert.Subject.CommonName
}
