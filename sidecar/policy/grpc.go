package policy

import (
	"fmt"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// The gRPC-specific rule type. One, not a family: method identity already
// travels through http_resource (a gRPC statement carries its un-normalized
// path as the resource) and through table (Tables carries the service name),
// so the only question those cannot ask is the RPC's outcome. gRPC puts that
// in the trailers as grpc-status, and :status is 200 on every live RPC, so
// http_status cannot express it either.
const (
	// MatchGRPCStatus matches the trailer statement of an RPC when its
	// grpc-status is in Statuses. Entries are lower-case names
	// ("permission_denied") or decimal codes ("7").
	//
	// Response-side only, and the response body has already reached the
	// client when the trailers arrive, so a deny here cannot retract data;
	// the lane replaces the final status with permission_denied.
	// `action: defer` is the usual pairing: record the original outcome as a
	// finding for OPA and the audit trail.
	MatchGRPCStatus MatchType = "grpc_status"
)

// validateGRPC checks the gRPC-specific fields at construction. It shares
// the Statuses field with http_status, because both mean "match the
// outcome"; only the vocabulary of specs differs.
func (r Rule) validateGRPC() error {
	if len(r.Statuses) == 0 {
		return fmt.Errorf("%s: grpc_status rule with no statuses", r.Name)
	}
	for _, s := range r.Statuses {
		if _, ok := inspect.GRPCStatusCode(s); !ok {
			return fmt.Errorf(
				"%s: invalid grpc status %q (want a name like permission_denied or a code 0..16)",
				r.Name, s)
		}
	}
	return nil
}

// matchesGRPC evaluates the gRPC rule types. ok reports whether this rule
// type belongs here, mirroring matchesHTTP: a statement without the status
// metadata (a request, or another protocol entirely) never matches, because
// failing closed would deny every SQL statement in a mixed rule set.
func (r Rule) matchesGRPC(stmt inspect.Statement) (matched, ok bool) {
	if r.Type != MatchGRPCStatus {
		return false, false
	}
	got, present := stmt.Metadata[inspect.MetadataGRPCStatusCode]
	if !present {
		return false, true // a request, or not a gRPC statement at all
	}
	code, valid := inspect.GRPCStatusCode(got)
	if !valid {
		return false, true
	}
	for _, spec := range r.Statuses {
		if want, ok := inspect.GRPCStatusCode(spec); ok && want == code {
			return true, true
		}
	}
	return false, true
}
