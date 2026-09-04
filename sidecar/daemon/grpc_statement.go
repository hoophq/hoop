package daemon

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// Statements a grpc lane emits, per ADR-0013:
//
//   - one FromClient statement when the request headers arrive, always;
//   - one FromServer statement when the trailers arrive, always;
//   - one FromClient/FromServer statement per decoded message, only when
//     the lane captures payloads.
//
// Operation is OpCall on every one of them. gRPC has no verb, and a
// method-name heuristic would classify GetAndPurge as a read; policy keys
// on identity (Tables, the un-normalized Resource) and on outcome
// (grpc_status) instead.

// splitGRPCMethod parses "/pkg.Service/Method". ok is false for anything else,
// which for a gRPC lane means a request no gRPC client sent.
func splitGRPCMethod(path string) (service, name string, ok bool) {
	if len(path) < 2 || path[0] != '/' {
		return "", "", false
	}
	rest := path[1:]
	i := strings.IndexByte(rest, '/')
	if i <= 0 || i == len(rest)-1 || strings.ContainsRune(rest[i+1:], '/') {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// laneStatements builds the statements for one RPC. It holds the per-request
// facts once so the request, message and trailer statements agree on them.
type laneStatements struct {
	path      string
	service   string
	method    string
	authority string

	metadataAllow []string // lower-cased header allowlist
	baseMeta      map[string]string
}

func newLaneStatements(r *http.Request, service, methodName string, allow []string) *laneStatements {
	s := &laneStatements{
		path:          r.URL.Path,
		service:       service,
		method:        methodName,
		authority:     r.Host,
		metadataAllow: allow,
	}

	meta := map[string]string{
		inspect.MetadataGRPCService:   service,
		inspect.MetadataGRPCMethod:    methodName,
		inspect.MetadataGRPCAuthority: r.Host,
	}
	if v := r.Header.Get("grpc-timeout"); v != "" {
		meta[inspect.MetadataGRPCTimeout] = v
	}
	if v := r.Header.Get("grpc-encoding"); v != "" && v != "identity" {
		meta[inspect.MetadataGRPCEncoding] = v
	}
	if v := r.Header.Get("grpc-message-type"); v != "" {
		meta[inspect.MetadataGRPCMessageType] = v
	}
	if v := r.Header.Get("user-agent"); v != "" {
		meta[inspect.MetadataGRPCUserAgent] = v
	}
	s.baseMeta = meta
	return s
}

// base assembles the fields every statement of this RPC shares. Tables
// carries the service and service/method the way the HTTP codec's
// resourceTables carries the resource, so a `table` rule fences a service
// with no new rule type. Resource is the path UN-normalized: the HTTP
// normalizer collapses long dotted service names, so a grpc statement never
// goes through it.
func (s *laneStatements) base(dir inspect.Direction, hdr http.Header) inspect.Statement {
	d := &inspect.HTTPDetail{
		Method:      "POST",
		Path:        s.path,
		Host:        s.authority,
		Resource:    s.path,
		ContentType: grpcContentTypeOf(hdr),
		Headers:     s.pickHeaders(hdr),
	}
	meta := make(map[string]string, len(s.baseMeta)+2)
	for k, v := range s.baseMeta {
		meta[k] = v
	}
	return inspect.Statement{
		Protocol:  inspect.GRPC,
		Direction: dir,
		Text:      s.path,
		Operation: inspect.OpCall,
		Tables: []string{
			strings.ToLower(s.service),
			strings.ToLower(s.service + "/" + s.method),
		},
		HTTP:     d,
		Metadata: meta,
	}
}

// request is the statement emitted at the request headers, before the
// upstream is dialed. Denying it costs the upstream nothing.
func (s *laneStatements) request(r *http.Request) inspect.Statement {
	return s.base(inspect.FromClient, r.Header)
}

// trailer is the statement emitted when the RPC's outcome exists. code is
// the numeric grpc-status; message its grpc-message, already decoded.
func (s *laneStatements) trailer(hdr http.Header, code int, message string) inspect.Statement {
	stmt := s.base(inspect.FromServer, hdr)
	// The transport's :status (200 on every live RPC) is deliberately NOT
	// copied into HTTP.StatusCode: it carries no outcome, and a non-zero
	// value would hand http_status rules something meaningless to match.
	// The RPC's outcome is the grpc-status below, matched by grpc_status.
	stmt.Metadata[inspect.MetadataGRPCStatusCode] = strconv.Itoa(code)
	if name := inspect.GRPCStatusText(code); name != "" {
		stmt.Metadata[inspect.MetadataGRPCStatus] = name
	}
	if message != "" {
		stmt.Metadata["grpc.message"] = message
	}
	return stmt
}

// message is a per-message statement, emitted only under payload capture.
// rendered is the protojson form of the decoded message, already truncated
// to the lane's budget; index is 1-based within the direction.
func (s *laneStatements) message(dir inspect.Direction, rendered string, truncated bool, index int) inspect.Statement {
	stmt := s.base(dir, nil)
	stmt.Text = s.path + "\n" + rendered
	stmt.HTTP.Body = rendered
	stmt.HTTP.BodyTruncated = truncated
	stmt.Metadata[inspect.MetadataGRPCMessageIndex] = strconv.Itoa(index)
	return stmt
}

// pickHeaders returns only the allowlisted metadata headers, nil when the
// allowlist is empty, so the field drops out of the policy input rather
// than serializing as {}. The daemon already refused the headers that must
// never travel (authorization and friends).
func (s *laneStatements) pickHeaders(h http.Header) map[string]string {
	if len(s.metadataAllow) == 0 || h == nil {
		return nil
	}
	var out map[string]string
	for _, name := range s.metadataAllow {
		if v := h.Get(name); v != "" {
			if out == nil {
				out = make(map[string]string, len(s.metadataAllow))
			}
			out[strings.ToLower(name)] = v
		}
	}
	return out
}

func grpcContentTypeOf(h http.Header) string {
	if h == nil {
		return ""
	}
	ct := h.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}
