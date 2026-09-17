package inspect_test

import (
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

func TestGRPCStatusVocabulary(t *testing.T) {
	for _, tt := range []struct {
		spec string
		code int
		ok   bool
	}{
		{spec: "permission_denied", code: 7, ok: true},
		{spec: "UNAUTHENTICATED", code: 16, ok: true},
		{spec: "0", code: 0, ok: true},
		{spec: "17", ok: false},
		{spec: "-1", ok: false},
		{spec: "permission denied", ok: false},
	} {
		got, ok := inspect.GRPCStatusCode(tt.spec)
		if got != tt.code || ok != tt.ok {
			t.Errorf("GRPCStatusCode(%q) = (%d, %v), want (%d, %v)",
				tt.spec, got, ok, tt.code, tt.ok)
		}
	}
	if got := inspect.GRPCStatusText(7); got != "permission_denied" {
		t.Fatalf("GRPCStatusText(7) = %q", got)
	}
	if got := inspect.GRPCStatusText(17); got != "" {
		t.Fatalf("GRPCStatusText(17) = %q", got)
	}
}

func TestGRPCHasNoByteCodec(t *testing.T) {
	if _, err := inspect.New(inspect.GRPC); err == nil {
		t.Fatal("grpc unexpectedly registered a byte-stream codec")
	}
}

// A gRPC statement carries protobuf renderings, not SQL, and the analyzer
// must say so rather than silently running the PostgreSQL lexer over
// protojson and reporting whatever relations it hallucinates.
func TestAnalyzeSQLFailsClosedForGRPC(t *testing.T) {
	got := inspect.AnalyzeSQL(`{"note":"DELETE FROM customers"}`, inspect.GRPC)
	if got.Complete {
		t.Fatal("grpc analysis reported a complete scan")
	}
	if got.Operation != inspect.OpUnknown {
		t.Fatalf("operation = %q, want unknown", got.Operation)
	}
	if got.Reason == "" {
		t.Fatal("fail-closed result carries no reason")
	}
	if len(got.Relations) != 0 || len(got.Tables) != 0 {
		t.Fatalf("grpc analysis invented relations: %+v", got)
	}
}

// An ssh statement is a command line, a variable name or a file path, and
// the PostgreSQL lexer reads all three without complaining. `rm -rf /` is
// not SQL, but nothing in a lexer says so, and the relations it returns
// would be read by a rule as if a human had named them.
//
// Nothing passes SSH here today. The branch exists so the first caller that
// does fails closed rather than acting on an invented answer.
func TestAnalyzeSQLFailsClosedForSSH(t *testing.T) {
	for _, text := range []string{
		"rm -rf /var/lib/postgresql",
		"/srv/customers.csv",
		"LD_PRELOAD",
		// The one that is not merely noise: the PostgreSQL lexer reads
		// this command as a complete UPDATE, which a read-only lane
		// would deny and an audit trail would record as a write.
		"update-alternatives --config editor",
	} {
		got := inspect.AnalyzeSQL(text, inspect.SSH)
		if got.Complete {
			t.Errorf("ssh analysis of %q reported a complete scan", text)
		}
		if got.Operation != inspect.OpUnknown {
			t.Errorf("operation for %q = %q, want unknown", text, got.Operation)
		}
		if got.Reason == "" {
			t.Errorf("fail-closed result for %q carries no reason", text)
		}
		if len(got.Relations) != 0 || len(got.Tables) != 0 {
			t.Errorf("ssh analysis of %q invented relations: %+v", text, got)
		}
	}
}
