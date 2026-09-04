package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
	codecgrpc "github.com/hoophq/libhoop/v2/codec/grpc"
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
	// (protoc --include_imports --descriptor_set_out, or buf build -o) —
	// a single path or a list; sets merge, byte-identical shared imports
	// dedupe, and conflicting copies of one file refuse at startup.
	// Multiple sets are the multi-team shape: each service's CI ships its
	// own artifact and no central re-bundle pipeline is required.
	// Required for any payload work: schema-less protobuf walking loses
	// values as a function of their bytes, so no capture, masking or PII
	// scanning happens without it (ADR-0013).
	Descriptors DescriptorPaths `json:"descriptors,omitempty"`

	// CapturePayload renders decoded request and response messages into
	// per-message Statements so payload-matching rules (pii, pattern_match)
	// and OPA can read them. Requires Descriptors.
	CapturePayload bool `json:"capture_payload"`

	// MaxPayloadBytes truncates a captured rendering. Zero uses the lane
	// default. Masking does not read this: it rewrites decoded fields
	// whatever their size.
	MaxPayloadBytes int `json:"max_payload_bytes,omitempty"`

	// Strict refuses an RPC whose payload cannot be read: a method the
	// descriptor set does not define is refused before the upstream is
	// dialed (FAILED_PRECONDITION), and a message that does not decode as
	// its declared type ends the RPC (INTERNAL). Off, the default, such
	// payloads are forwarded with method-level inspection only, and each
	// degradation is logged. A lane with mask rules fails closed either
	// way: a redactor must not forward what it cannot decode.
	Strict bool `json:"strict,omitempty"`

	// Metadata names the request metadata headers to expose to policy,
	// matched case-insensitively. There is no capture-all, and the same
	// headers HTTPCodecConfig refuses are refused here; authorization is
	// where a gRPC bearer token lives.
	Metadata []string `json:"metadata,omitempty"`
}

func (g *GRPCCodecConfig) validate(lane string) []string {
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
		}
	}
	if g.CapturePayload && len(g.Descriptors) == 0 {
		// Without a schema the lane cannot decode a message soundly, so a
		// capture flag would produce nothing and every rule reading payloads
		// would silently never fire: the failure this package refuses
		// everywhere else.
		problems = append(problems, fmt.Sprintf(
			"listener %q: grpc.capture_payload needs grpc.descriptors; without a "+
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

// hasDescriptors reports whether the lane can decode payloads, tolerating a
// nil receiver so call sites read as one condition.
func (g *GRPCCodecConfig) hasDescriptors() bool {
	return g != nil && len(g.Descriptors) > 0
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

const grpcPermissionDenied = 7

// buildGRPCServer resolves one grpc lane's transport facts and builds the
// reusable libhoop endpoint with sidecar-owned RPC callbacks.
func buildGRPCServer(
	ln lane,
	ac AuditConfig,
	sink audit.Sink,
	log *slog.Logger,
) (GRPCServer, error) {
	lc := ln.cfg
	var gc GRPCCodecConfig
	if lc.GRPC != nil {
		gc = *lc.GRPC
	}

	laneLog := log.With("listener", ln.name)
	allowedMetadata := make([]string, 0, len(gc.Metadata))
	for _, name := range gc.Metadata {
		allowedMetadata = append(allowedMetadata, strings.ToLower(strings.TrimSpace(name)))
	}
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
	if len(gc.Descriptors) > 0 {
		// The list travels as one comma-separated setting; the escape
		// discipline (\\ then \,) is libhoop's, so any filesystem path —
		// commas and backslashes included — survives the seam.
		escaped := make([]string, len(gc.Descriptors))
		for i, p := range gc.Descriptors {
			p = strings.ReplaceAll(p, `\`, `\\`)
			escaped[i] = strings.ReplaceAll(p, ",", `\,`)
		}
		opts["descriptors"] = strings.Join(escaped, ",")
	}
	if gc.CapturePayload {
		opts["capture_payload"] = "true"
	}
	if gc.MaxPayloadBytes > 0 {
		opts["max_payload_bytes"] = strconv.Itoa(gc.MaxPayloadBytes)
	}
	if gc.Strict {
		opts["strict"] = "true"
	}
	if ln.masker != nil {
		opts["mask_responses"] = "true"
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

	open := func(ctx context.Context, info codecgrpc.RPCInfo) (*codecgrpc.RPCHandler, *codecgrpc.Status, error) {
		identity := session.Identity{PeerAddr: info.Request.RemoteAddr}
		if lc.IdentityHeader != "" {
			identity.Subject = info.Request.Header.Get(lc.IdentityHeader)
		}
		if identity.Subject == "" {
			identity.Subject = grpcPeerSubject(info.Request)
		}

		sess := session.New(inspect.GRPC, identity)
		sess.Connection = ln.name
		sess.Upstream = lc.Upstream
		g, err := gate.NewStatementGate(sess, gate.Config{
			Protocol:         inspect.GRPC,
			Policy:           ln.policy,
			Audit:            sink,
			Masker:           ln.masker,
			FailOnAuditError: failOnAuditError,
		})
		if err != nil {
			return nil, nil, err
		}

		state := &grpcRPCState{
			gate:  g,
			stmts: newLaneStatements(info.Request, info.Service, info.Method, allowedMetadata),
			log:   laneLog,
		}
		handler := state.callbacks()
		if err := g.Start(ctx); err != nil {
			if failOnAuditError {
				return handler, &codecgrpc.Status{
					Code:    13,
					Message: "audit trail unavailable; RPC refused",
				}, nil
			}
			laneLog.Warn("grpc session start not recorded", "error", err)
		}

		d := g.EvaluateStatement(ctx, state.stmts.request(info.Request))
		state.logDecisionError("request headers", d)
		if !d.Allowed {
			return handler, grpcDeniedStatus(d.Message), nil
		}
		return handler, nil, nil
	}

	srv, err := codecgrpc.NewServer(opts, open, laneLog)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ln.name, err)
	}
	return srv, nil
}

type grpcRPCState struct {
	gate  *gate.Gate
	stmts *laneStatements
	log   *slog.Logger
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
	ctx context.Context,
	rendered string,
	truncated bool,
	index int,
) *codecgrpc.Status {
	d := r.gate.EvaluateStatement(ctx,
		r.stmts.message(inspect.FromClient, rendered, truncated, index))
	r.logDecisionError("request message", d)
	if !d.Allowed {
		return grpcDeniedStatus(d.Message)
	}
	return nil
}

func (r *grpcRPCState) responseMessage(
	ctx context.Context,
	rendered string,
	truncated bool,
	index int,
) *codecgrpc.Status {
	d := r.gate.EvaluateStatement(ctx,
		r.stmts.message(inspect.FromServer, rendered, truncated, index))
	r.logDecisionError("response message", d)
	if !d.Allowed {
		return grpcDeniedStatus(d.Message)
	}
	return nil
}

func (r *grpcRPCState) responseStatus(
	ctx context.Context,
	trailers http.Header,
	code int,
	message string,
) *codecgrpc.Status {
	d := r.gate.EvaluateStatement(context.WithoutCancel(ctx),
		r.stmts.trailer(trailers, code, message))
	r.logDecisionError("response trailers", d)
	if !d.Allowed {
		return grpcDeniedStatus(d.Message)
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
	err := r.gate.Close(context.WithoutCancel(ctx))
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

func grpcDeniedStatus(message string) *codecgrpc.Status {
	return &codecgrpc.Status{Code: grpcPermissionDenied, Message: message}
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
