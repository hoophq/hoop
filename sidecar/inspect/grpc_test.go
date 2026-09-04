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
