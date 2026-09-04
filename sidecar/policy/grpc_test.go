package policy

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

func TestGRPCStatusRule(t *testing.T) {
	rules, err := NewRules([]Rule{{
		Name: "auth-failure", Type: MatchGRPCStatus,
		httpRuleFields: httpRuleFields{
			Statuses: []string{"permission_denied", "16"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name   string
		status string
		deny   bool
	}{
		{name: "named match", status: "7", deny: true},
		{name: "numeric spec match", status: "16", deny: true},
		{name: "different status", status: "0", deny: false},
		{name: "request has no status", status: "", deny: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			metadata := map[string]string{}
			if tt.status != "" {
				metadata[inspect.MetadataGRPCStatusCode] = tt.status
			}
			got := rules.Evaluate(inspect.Statement{Protocol: inspect.GRPC, Metadata: metadata})
			if got.Denied != tt.deny {
				t.Fatalf("Denied = %v, want %v", got.Denied, tt.deny)
			}
		})
	}
}

// gRPC rides HTTP/2, so a trailer statement could carry the transport's
// synthetic 200. http_status must never read that as an outcome: a 2xx
// rule would fire on every RPC completion. The same rule keeps matching
// real HTTP, and http_resource keeps matching gRPC method identity.
func TestHTTPStatusRuleIgnoresGRPCStatements(t *testing.T) {
	rules, err := NewRules([]Rule{{
		Name: "no-2xx", Type: MatchHTTPStatus,
		httpRuleFields: httpRuleFields{Statuses: []string{"2xx"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	grpcTrailer := inspect.Statement{
		Protocol:  inspect.GRPC,
		Direction: inspect.FromServer,
		HTTP:      &inspect.HTTPDetail{Method: "POST", Resource: "/billing.v1.Invoices/Get", StatusCode: 200},
		Metadata:  map[string]string{inspect.MetadataGRPCStatusCode: "0"},
	}
	if got := rules.Evaluate(grpcTrailer); got.Denied {
		t.Fatalf("http_status rule fired on a gRPC completion: %+v", got)
	}

	httpResponse := inspect.Statement{
		Protocol:  inspect.HTTP,
		Direction: inspect.FromServer,
		HTTP:      &inspect.HTTPDetail{Method: "GET", Resource: "/users/*", StatusCode: 200},
	}
	if got := rules.Evaluate(httpResponse); !got.Denied {
		t.Fatal("the guard must be protocol-scoped: the same rule no longer matches HTTP")
	}
}

func TestHTTPResourceRuleStillMatchesGRPCMethods(t *testing.T) {
	rules, err := NewRules([]Rule{{
		Name: "no-bulk-export", Type: MatchHTTPResource,
		httpRuleFields: httpRuleFields{Resources: []string{"/billing.v1.Invoices/ExportAll"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	stmt := inspect.Statement{
		Protocol: inspect.GRPC,
		HTTP:     &inspect.HTTPDetail{Method: "POST", Resource: "/billing.v1.Invoices/ExportAll"},
	}
	if got := rules.Evaluate(stmt); !got.Denied {
		t.Fatal("http_resource must keep matching gRPC method identity")
	}
}

func TestGRPCStatusRuleRejectsUnknownStatus(t *testing.T) {
	_, err := NewRules([]Rule{{
		Name: "bad-status", Type: MatchGRPCStatus,
		httpRuleFields: httpRuleFields{
			Statuses: []string{"not_a_status"},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "invalid grpc status") {
		t.Fatalf("error = %v", err)
	}
}
