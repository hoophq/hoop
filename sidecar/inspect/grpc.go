package inspect

// The gRPC vocabulary beyond the Protocol constant.
//
// GRPC itself is defined in libhoop's types package beside the other
// protocols. wiretypes.go gives the same shared Protocol type and stable wire
// value a local name. It is the one protocol constant with no registry decoder
// behind it, and that is deliberate (ADR-0013): the
// bytes-in/statements-out Codec shape
// cannot deny a multi-read message, cannot deny one stream of a multiplexed
// connection, and cannot mask a protocol whose length prefixes nest. A gRPC
// lane terminates HTTP/2 in-process instead and enters the gate at statement
// granularity. Reusable descriptor, framing, masking, and status mechanics
// live in libhoop/v2/codec/grpc, but there is no decoder for the registry to
// hand out.
//
// Consequence: inspect.New(GRPC) returns ErrUnsupportedProtocol, on purpose.
// The HTTP/2 endpoint lives in libhoop/v2/codec/grpc; daemon injects the
// sidecar's statement, policy, audit, and masking callbacks.

// Metadata keys a gRPC statement carries. They are the policy surface for
// facts that have no field on Statement or HTTPDetail: OPA reads them from
// input.metadata, and the grpc_status rule type reads the status pair.
//
// A request statement carries the identity and header keys. The trailer
// statement adds the status pair, because that is when the outcome exists:
// gRPC puts the result in the trailers and answers 200 on every live RPC.
const (
	// MetadataGRPCService is the fully qualified service, "billing.v1.Invoices".
	MetadataGRPCService = "grpc.service"

	// MetadataGRPCMethod is the bare method name, "GetInvoice".
	MetadataGRPCMethod = "grpc.method"

	// MetadataGRPCAuthority is the :authority pseudo-header.
	MetadataGRPCAuthority = "grpc.authority"

	// MetadataGRPCTimeout is the grpc-timeout header verbatim ("10S"),
	// present only when the client sent a deadline.
	MetadataGRPCTimeout = "grpc.timeout"

	// MetadataGRPCEncoding is the grpc-encoding header, present when not
	// identity.
	MetadataGRPCEncoding = "grpc.encoding"

	// MetadataGRPCMessageType is the grpc-message-type header, a free
	// schema hint some clients send.
	MetadataGRPCMessageType = "grpc.message_type"

	// MetadataGRPCUserAgent is the client's user-agent ("grpc-go/1.83.0").
	MetadataGRPCUserAgent = "grpc.user_agent"

	// MetadataGRPCStatusCode is the numeric grpc-status from the trailers,
	// "0".."16". Present only on the trailer statement.
	MetadataGRPCStatusCode = "grpc.status_code"

	// MetadataGRPCStatus is the lower-case name for the same code,
	// "permission_denied". Present only on the trailer statement.
	MetadataGRPCStatus = "grpc.status"

	// MetadataGRPCMessageIndex is the 1-based ordinal of a per-message
	// statement within its RPC. Present only when payload capture is on.
	MetadataGRPCMessageIndex = "grpc.message_index"
)

// grpcStatusNames maps the 17 canonical gRPC status codes to their
// lower-case names, the spelling both the metadata and a grpc_status rule
// use. Lower-case because rules are YAML and the audit trail is grep'd;
// OK/NOT_FOUND casing would make every rule a case-folding question.
var grpcStatusNames = [...]string{
	0:  "ok",
	1:  "cancelled",
	2:  "unknown",
	3:  "invalid_argument",
	4:  "deadline_exceeded",
	5:  "not_found",
	6:  "already_exists",
	7:  "permission_denied",
	8:  "resource_exhausted",
	9:  "failed_precondition",
	10: "aborted",
	11: "out_of_range",
	12: "unimplemented",
	13: "internal",
	14: "unavailable",
	15: "data_loss",
	16: "unauthenticated",
}

// GRPCStatusText returns the lower-case name for a gRPC status code, or ""
// for a code outside 0..16. The empty string, not a rendering of the number:
// a caller writing metadata must not invent a name a rule could never spell.
func GRPCStatusText(code int) string {
	if code < 0 || code >= len(grpcStatusNames) {
		return ""
	}
	return grpcStatusNames[code]
}

// GRPCStatusCode resolves a status spec as a rule writes it: a lower-case
// name ("permission_denied", any case accepted) or a decimal code ("7").
// ok is false for anything else, so a config typo is refused at load rather
// than compiled into a rule that never matches.
func GRPCStatusCode(spec string) (code int, ok bool) {
	if spec == "" {
		return 0, false
	}
	if c, numeric := atoiStrict(spec); numeric {
		if c < 0 || c >= len(grpcStatusNames) {
			return 0, false
		}
		return c, true
	}
	folded := asciiLower(spec)
	for c, name := range grpcStatusNames {
		if folded == name {
			return c, true
		}
	}
	return 0, false
}

// atoiStrict parses a small non-negative decimal. strconv would work; this
// keeps the file free of an import for two-digit numbers on a cold path.
func atoiStrict(s string) (int, bool) {
	if s == "" || len(s) > 2 {
		return 0, false
	}
	n := 0
	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
