package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

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
	// Descriptors is the path to a serialized FileDescriptorSet
	// (protoc --include_imports --descriptor_set_out, or buf build -o).
	// Required for any payload work: schema-less protobuf walking loses
	// values as a function of their bytes, so no capture, masking or PII
	// scanning happens without it (ADR-0013).
	Descriptors string `json:"descriptors,omitempty"`

	// CapturePayload renders decoded request and response messages into
	// per-message Statements so payload-matching rules (pii, pattern_match)
	// and OPA can read them. Requires Descriptors.
	CapturePayload bool `json:"capture_payload"`

	// MaxPayloadBytes truncates a captured rendering. Zero uses the lane
	// default. Masking does not read this: it rewrites decoded fields
	// whatever their size.
	MaxPayloadBytes int `json:"max_payload_bytes,omitempty"`

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
	if g.CapturePayload && g.Descriptors == "" {
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

// descriptors reports the configured descriptor set path, tolerating a nil
// receiver so call sites read as one condition.
func (g *GRPCCodecConfig) descriptors() string {
	if g == nil {
		return ""
	}
	return g.Descriptors
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
	upstreamTLS, err := lc.UpstreamTLS.BuildTLS()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ln.name, err)
	}
	if upstreamTLS != nil && upstreamTLS.InsecureSkipVerify {
		log.Warn("upstream certificate verification is DISABLED",
			"listener", ln.name, "upstream", lc.Upstream)
	}
	downstreamTLS, err := lc.DownstreamTLS.BuildDownstreamTLS()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ln.name, err)
	}

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

	return codecgrpc.NewServer(codecgrpc.Config{
		Name:            ln.name,
		Listen:          lc.Listen,
		Network:         lc.Network,
		Upstream:        lc.Upstream,
		UpstreamTLS:     upstreamTLS,
		DownstreamTLS:   downstreamTLS,
		IdleTimeout:     time.Duration(lc.IdleTimeoutSec) * time.Second,
		MaxConns:        lc.MaxConns,
		Descriptors:     gc.Descriptors,
		CapturePayload:  gc.CapturePayload,
		MaxPayloadBytes: gc.MaxPayloadBytes,
		Log:             laneLog,
		MaskResponses:   ln.masker != nil,
		Open: func(ctx context.Context, info codecgrpc.RPCInfo) (*codecgrpc.RPCHandler, *codecgrpc.Status, error) {
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
		},
	})
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
