package daemon

import (
	"bytes"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	codecgrpc "github.com/hoophq/libhoop/v2/codec/grpc"
)

// The method table itself, without a server: which methods yield SQL, both
// JSON spellings for the CreateDatabase fields, and the fail-CLOSED
// contract: an unknown method or service is not sqlBearing (generic
// statement territory), while a SQL-bearing method whose rendering does
// not parse stays sqlBearing with no SQL — the caller must turn that into
// OpUnknown, never the generic OpCall statement, or truncating the capture
// becomes a policy bypass.
func TestSpannerSQLStatements(t *testing.T) {
	cases := []struct {
		name       string
		service    string
		method     string
		rendered   string
		want       []string
		sqlBearing bool
	}{
		{
			name:    "ExecuteSql",
			service: "google.spanner.v1.Spanner", method: "ExecuteSql",
			rendered: `{"sql":"SELECT 1"}`,
			want:     []string{"SELECT 1"}, sqlBearing: true,
		},
		{
			name:    "ExecuteStreamingSql",
			service: "google.spanner.v1.Spanner", method: "ExecuteStreamingSql",
			rendered: `{"session":"s","sql":"SELECT id FROM t"}`,
			want:     []string{"SELECT id FROM t"}, sqlBearing: true,
		},
		{
			name:    "PartitionQuery",
			service: "google.spanner.v1.Spanner", method: "PartitionQuery",
			rendered: `{"sql":"SELECT * FROM big"}`,
			want:     []string{"SELECT * FROM big"}, sqlBearing: true,
		},
		{
			name:    "ExecuteBatchDml",
			service: "google.spanner.v1.Spanner", method: "ExecuteBatchDml",
			rendered: `{"statements":[{"sql":"UPDATE t SET a = 1"},{"sql":"DELETE FROM t"}]}`,
			want:     []string{"UPDATE t SET a = 1", "DELETE FROM t"}, sqlBearing: true,
		},
		{
			name:    "UpdateDatabaseDdl",
			service: "google.spanner.admin.database.v1.DatabaseAdmin", method: "UpdateDatabaseDdl",
			rendered: `{"statements":["CREATE TABLE t (id INT64) PRIMARY KEY (id)","DROP TABLE old"]}`,
			want:     []string{"CREATE TABLE t (id INT64) PRIMARY KEY (id)", "DROP TABLE old"}, sqlBearing: true,
		},
		{
			name:    "CreateDatabase proto names",
			service: "google.spanner.admin.database.v1.DatabaseAdmin", method: "CreateDatabase",
			rendered: `{"create_statement":"CREATE DATABASE db","extra_statements":["CREATE TABLE t (id INT64) PRIMARY KEY (id)"]}`,
			want:     []string{"CREATE DATABASE db", "CREATE TABLE t (id INT64) PRIMARY KEY (id)"}, sqlBearing: true,
		},
		{
			name:    "CreateDatabase lowerCamel",
			service: "google.spanner.admin.database.v1.DatabaseAdmin", method: "CreateDatabase",
			rendered: `{"createStatement":"CREATE DATABASE db","extraStatements":["DROP TABLE t"]}`,
			want:     []string{"CREATE DATABASE db", "DROP TABLE t"}, sqlBearing: true,
		},
		{
			name:    "unknown method",
			service: "google.spanner.v1.Spanner", method: "Commit",
			rendered: `{"transaction_tag":"tx"}`,
			want:     nil,
		},
		{
			name:    "unknown service",
			service: "test.v1.Echo", method: "ExecuteSql",
			rendered: `{"sql":"SELECT 1"}`,
			want:     nil,
		},
		{
			name:    "truncated rendering stays sqlBearing",
			service: "google.spanner.v1.Spanner", method: "ExecuteSql",
			rendered: `{"sql":"SELECT`,
			want:     nil, sqlBearing: true,
		},
		{
			name:    "parseable but empty stays sqlBearing",
			service: "google.spanner.v1.Spanner", method: "ExecuteBatchDml",
			rendered: `{"statements":[]}`,
			want:     nil, sqlBearing: true,
		},
	}
	for _, tc := range cases {
		got, sqlBearing := spannerSQLStatements(tc.service, tc.method, tc.rendered)
		if sqlBearing != tc.sqlBearing {
			t.Errorf("%s: sqlBearing = %v, want %v", tc.name, sqlBearing, tc.sqlBearing)
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: statement %d = %q, want %q", tc.name, i, got[i], tc.want[i])
			}
		}
	}
}

// Case (a) and (e): a SELECT through ExecuteSql passes a deny-delete
// policy, the upstream sees byte-identical frames, and the audit trail
// carries the SQL statement with Protocol spanner, the extracted text, the
// classifier's operation and the spanner.sql_index ordinal.
func TestSpannerLaneAllowsSelectForwardsBytesAndAuditsSQL(t *testing.T) {
	descriptorPath := writeSpannerTestDescriptors(t)
	requestWire := marshalSpannerSQLRequest("SELECT id FROM accounts")
	upstreamRequests := make(chan []byte, 1)
	upstreamAddr, stopUpstream := startGRPCTestH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read request: %v", readErr)
			return
		}
		upstreamRequests <- body
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Grpc-Status", "0")
	}))
	defer stopUpstream()

	sink := newGRPCTestMemorySink()
	server := buildSpannerTestServer(t, "spanner-select", upstreamAddr,
		&GRPCCodecConfig{Descriptors: DescriptorPaths{descriptorPath}, CapturePayload: true},
		spannerTestDenyDelete{}, sink)
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	resp := spannerTestRoundTrip(t, laneAddr, "ExecuteSql", grpcTestFrame(0, requestWire))
	if got := spannerTestStatus(resp); got != "0" {
		t.Fatalf("grpc-status = %q, want 0", got)
	}

	select {
	case got := <-upstreamRequests:
		if !bytes.Equal(got, grpcTestFrame(0, requestWire)) {
			t.Fatalf("upstream request changed: %x", got)
		}
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive the request")
	}

	select {
	case <-sink.ended:
	case <-time.After(time.Second):
		t.Fatal("session end was not audited")
	}
	var sawSQL bool
	for _, event := range sink.snapshot() {
		if event.Metadata["spanner.sql_index"] == "" {
			continue
		}
		sawSQL = true
		if event.Protocol != inspect.Spanner {
			t.Errorf("SQL statement protocol = %q, want spanner", event.Protocol)
		}
		if event.Statement != "SELECT id FROM accounts" {
			t.Errorf("SQL statement text = %q", event.Statement)
		}
		if event.Operation != inspect.OpSelect {
			t.Errorf("SQL statement operation = %q, want select", event.Operation)
		}
		if event.Metadata["spanner.sql_index"] != "1" {
			t.Errorf("spanner.sql_index = %q, want 1", event.Metadata["spanner.sql_index"])
		}
		if !event.Allowed {
			t.Error("SELECT was denied by a deny-delete policy")
		}
	}
	if !sawSQL {
		t.Fatal("no audited statement carries spanner.sql_index")
	}
}

// Case (b): a DELETE through ExecuteSql is refused with grpc-status 7. The
// denial happens at the request MESSAGE — the SQL is inside the payload,
// which does not exist at the request headers — so the upstream connection
// may already be open; what the deny guarantees is that the frame carrying
// the DELETE never reaches it.
func TestSpannerLaneDeniesDeleteAtRequestMessage(t *testing.T) {
	descriptorPath := writeSpannerTestDescriptors(t)
	type upstreamRead struct {
		body []byte
		err  error
	}
	upstreamReads := make(chan upstreamRead, 1)
	upstreamAddr, stopUpstream := startGRPCTestH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		upstreamReads <- upstreamRead{body: body, err: readErr}
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Grpc-Status", "0")
	}))
	defer stopUpstream()

	server := buildSpannerTestServer(t, "spanner-deny", upstreamAddr,
		&GRPCCodecConfig{Descriptors: DescriptorPaths{descriptorPath}, CapturePayload: true},
		spannerTestDenyDelete{}, nil)
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	resp := spannerTestRoundTrip(t, laneAddr, "ExecuteSql",
		grpcTestFrame(0, marshalSpannerSQLRequest("DELETE FROM accounts WHERE id = 7")))
	if got := spannerTestStatus(resp); got != "7" {
		t.Fatalf("grpc-status = %q, want 7", got)
	}
	if got := codecgrpc.DecodeMessage(resp.Header.Get("Grpc-Message")); got != "deletes are refused by test policy" {
		t.Fatalf("grpc-message = %q", got)
	}
	select {
	case got := <-upstreamReads:
		if len(got.body) != 0 {
			t.Fatalf("denied DELETE reached upstream: %x", got.body)
		}
		if got.err == nil {
			t.Fatal("upstream saw a clean EOF for a denied request")
		}
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe the refused request stream")
	}
}

// Case (c): ExecuteBatchDml carries several SQL strings in one message and
// each is evaluated on its own, in order. The UPDATE passes and is audited
// as sql_index 1; the DELETE denies the whole message as sql_index 2,
// because the batch is atomic upstream and forwarding "the allowed half"
// of an all-or-nothing request is not a thing.
func TestSpannerLaneBatchDmlDeniesOnSecondStatement(t *testing.T) {
	descriptorPath := writeSpannerTestDescriptors(t)
	upstreamAddr, stopUpstream := startGRPCTestH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Grpc-Status", "0")
	}))
	defer stopUpstream()

	sink := newGRPCTestMemorySink()
	server := buildSpannerTestServer(t, "spanner-batch", upstreamAddr,
		&GRPCCodecConfig{Descriptors: DescriptorPaths{descriptorPath}, CapturePayload: true},
		spannerTestDenyDelete{}, sink)
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	resp := spannerTestRoundTrip(t, laneAddr, "ExecuteBatchDml",
		grpcTestFrame(0, marshalSpannerBatchDML(
			"UPDATE accounts SET name = 'x' WHERE id = 1",
			"DELETE FROM accounts")))
	if got := spannerTestStatus(resp); got != "7" {
		t.Fatalf("grpc-status = %q, want 7", got)
	}

	// The trail must show both members: the allowed UPDATE at index 1 and
	// the denied DELETE at index 2. One event for the batch would leave an
	// operator unable to say WHICH statement a denial names.
	deadline := time.After(time.Second)
	for {
		byIndex := map[string]bool{}
		for _, event := range sink.snapshot() {
			if i := event.Metadata["spanner.sql_index"]; i != "" {
				byIndex[i] = event.Allowed
			}
		}
		if len(byIndex) == 2 {
			if !byIndex["1"] {
				t.Error("the UPDATE at sql_index 1 was denied")
			}
			if byIndex["2"] {
				t.Error("the DELETE at sql_index 2 was allowed")
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("audited SQL statements by index = %v, want indexes 1 and 2", byIndex)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// Case (d): a method the extraction table does not know (Commit) keeps the
// generic per-message statement: OpCall, the rendered protojson as the
// body, no spanner.sql_index — and Protocol spanner throughout, so the
// analyzer's builder and any protocol-scoped policy see the lane
// consistently.
func TestSpannerLaneNonSQLMethodKeepsGenericStatement(t *testing.T) {
	descriptorPath := writeSpannerTestDescriptors(t)
	upstreamAddr, stopUpstream := startGRPCTestH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Grpc-Status", "0")
	}))
	defer stopUpstream()

	recorder := &spannerTestRecorder{}
	server := buildSpannerTestServer(t, "spanner-commit", upstreamAddr,
		&GRPCCodecConfig{Descriptors: DescriptorPaths{descriptorPath}, CapturePayload: true},
		recorder, nil)
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	resp := spannerTestRoundTrip(t, laneAddr, "Commit",
		grpcTestFrame(0, marshalSpannerSQLRequest("tx-tag")))
	if got := spannerTestStatus(resp); got != "0" {
		t.Fatalf("grpc-status = %q, want 0", got)
	}

	var message *inspect.Statement
	for _, stmt := range recorder.snapshot() {
		if stmt.Protocol != inspect.Spanner {
			t.Errorf("statement protocol = %q, want spanner", stmt.Protocol)
		}
		if stmt.Direction == inspect.FromClient && stmt.HTTP != nil && stmt.HTTP.Body != "" {
			s := stmt
			message = &s
		}
	}
	if message == nil {
		t.Fatal("no generic message statement was evaluated")
	}
	if message.Operation != inspect.OpCall {
		t.Errorf("message operation = %q, want call", message.Operation)
	}
	if _, ok := message.Metadata["spanner.sql_index"]; ok {
		t.Error("non-SQL message statement carries spanner.sql_index")
	}
	if !strings.HasPrefix(message.Text, "/google.spanner.v1.Spanner/Commit\n") {
		t.Errorf("message text = %q, want the generic path+rendering shape", message.Text)
	}
	if !strings.Contains(message.HTTP.Body, "tx-tag") {
		t.Errorf("message body = %q, does not carry the rendered payload", message.HTTP.Body)
	}
}

// The capture budget must not be a policy bypass. A rendering truncated at
// max_payload_bytes cannot be parsed for its SQL, and before the
// fail-closed branch existed that fell back to the generic OpCall
// statement — which an operation rule never matches, so padding a request
// past the budget smuggled any DML through. Now a SQL-bearing method whose
// rendering yields no SQL is evaluated as OpUnknown with the reason on the
// statement, and a policy naming unknown refuses it.
func TestSpannerLaneFailsClosedWhenCaptureTruncatesTheSQL(t *testing.T) {
	descriptorPath := writeSpannerTestDescriptors(t)
	upstreamAddr, stopUpstream := startGRPCTestH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Grpc-Status", "0")
	}))
	defer stopUpstream()

	// 16 bytes truncates `{"sql":"DELETE ...` mid-string: json.Unmarshal
	// fails, extraction yields nothing, and only the fail-closed branch
	// stands between this frame and the upstream.
	deny := &spannerTestDenyUnknown{}
	server := buildSpannerTestServer(t, "spanner-truncated", upstreamAddr,
		&GRPCCodecConfig{Descriptors: DescriptorPaths{descriptorPath}, CapturePayload: true,
			MaxPayloadBytes: 16},
		deny, nil)
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	resp := spannerTestRoundTrip(t, laneAddr, "ExecuteSql",
		grpcTestFrame(0, marshalSpannerSQLRequest("DELETE FROM accounts WHERE id = 7")))
	if got := spannerTestStatus(resp); got != "7" {
		t.Fatalf("grpc-status = %q, want 7 (truncated capture must fail closed)", got)
	}
	if got := codecgrpc.DecodeMessage(resp.Header.Get("Grpc-Message")); got != "unreadable statements are refused by test policy" {
		t.Fatalf("grpc-message = %q", got)
	}

	// The statement policy refused must say WHY it was unreadable, or the
	// operator debugging a refused batch job has nothing to act on.
	var unknown *inspect.Statement
	for _, s := range deny.snapshot() {
		if s.Operation == inspect.OpUnknown {
			c := s
			unknown = &c
			break
		}
	}
	if unknown == nil {
		t.Fatal("policy never saw an OpUnknown statement for the truncated capture")
	}
	if !strings.Contains(unknown.Metadata[inspect.MetadataSQLIncomplete], "truncated") {
		t.Fatalf("sql.incomplete = %q, want a truncation reason",
			unknown.Metadata[inspect.MetadataSQLIncomplete])
	}
}

// buildSpannerTestServer is buildGRPCTestServer with the spanner protocol
// value: same transport, same descriptor plumbing, statements carrying
// inspect.Spanner.
func buildSpannerTestServer(
	t *testing.T,
	name string,
	upstream string,
	grpcConfig *GRPCCodecConfig,
	evaluator policy.Evaluator,
	sink audit.Sink,
) GRPCServer {
	t.Helper()
	server, err := buildGRPCServer(lane{
		cfg: ListenerConfig{
			Name:     name,
			Protocol: "spanner",
			Listen:   "127.0.0.1:0",
			Upstream: upstream,
			GRPC:     grpcConfig,
		},
		name:   name,
		policy: evaluator,
	}, AuditConfig{}, sink, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func spannerTestRoundTrip(t *testing.T, laneAddr, method string, body []byte) *http.Response {
	t.Helper()
	transport := grpcTestTransport()
	t.Cleanup(transport.CloseIdleConnections)
	req, err := http.NewRequest(http.MethodPost,
		"http://"+laneAddr+"/google.spanner.v1.Spanner/"+method, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("Te", "trailers")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp
}

// spannerTestStatus reads grpc-status from wherever this response carried
// it: the trailers on a response with a body, the headers on a
// trailers-only response (which is what a local denial produces).
func spannerTestStatus(resp *http.Response) string {
	if s := resp.Trailer.Get("Grpc-Status"); s != "" {
		return s
	}
	return resp.Header.Get("Grpc-Status")
}

// spannerTestDescriptorSet is a serialized google.protobuf.FileDescriptorSet:
// google.spanner.v1.Spanner with ExecuteSql (request field sql=1 string),
// ExecuteBatchDml (statements=1, repeated message with sql=1 string) and
// Commit (transaction_tag=1 string), all returning an empty ResultSet.
// Checked in as bytes so this package needs no protobuf dependency;
// regenerate by marshalling a descriptorpb.FileDescriptorSet of that shape.
const spannerTestDescriptorSet = "CrEEChJzcGFubmVyX3Rlc3QucHJvdG8SEWdvb2dsZS5zcGFubmVyLnYxIiUKEUV4ZWN1dGVTcWxSZXF1ZXN0EhAKA3NxbBgBIAEoCVIDc3FsIowBChZFeGVjdXRlQmF0Y2hEbWxSZXF1ZXN0ElMKCnN0YXRlbWVudHMYASADKAsyMy5nb29nbGUuc3Bhbm5lci52MS5FeGVjdXRlQmF0Y2hEbWxSZXF1ZXN0LlN0YXRlbWVudFIKc3RhdGVtZW50cxodCglTdGF0ZW1lbnQSEAoDc3FsGAEgASgJUgNzcWwiOQoNQ29tbWl0UmVxdWVzdBIoCg90cmFuc2FjdGlvbl90YWcYASABKAlSD3RyYW5zYWN0aW9uX3RhZyILCglSZXN1bHRTZXQygQIKB1NwYW5uZXISUAoKRXhlY3V0ZVNxbBIkLmdvb2dsZS5zcGFubmVyLnYxLkV4ZWN1dGVTcWxSZXF1ZXN0GhwuZ29vZ2xlLnNwYW5uZXIudjEuUmVzdWx0U2V0EloKD0V4ZWN1dGVCYXRjaERtbBIpLmdvb2dsZS5zcGFubmVyLnYxLkV4ZWN1dGVCYXRjaERtbFJlcXVlc3QaHC5nb29nbGUuc3Bhbm5lci52MS5SZXN1bHRTZXQSSAoGQ29tbWl0EiAuZ29vZ2xlLnNwYW5uZXIudjEuQ29tbWl0UmVxdWVzdBocLmdvb2dsZS5zcGFubmVyLnYxLlJlc3VsdFNldGIGcHJvdG8z"

func writeSpannerTestDescriptors(t *testing.T) string {
	t.Helper()
	blob, err := base64.StdEncoding.DecodeString(spannerTestDescriptorSet)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/spanner.pb"
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	schema, err := codecgrpc.LoadSchema(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := schema.Lookup("/google.spanner.v1.Spanner/ExecuteSql"); !ok {
		t.Fatal("fixture method missing")
	}
	return path
}

// marshalSpannerSQLRequest hand-encodes one string into field 1, the shape
// of ExecuteSqlRequest.sql and CommitRequest.transaction_tag alike. Values
// must fit a single-byte varint length.
func marshalSpannerSQLRequest(value string) []byte {
	if len(value) > 127 {
		panic("spanner test values must fit a single-byte varint length")
	}
	out := make([]byte, 0, 2+len(value))
	out = append(out, 0x0a, byte(len(value)))
	return append(out, value...)
}

// marshalSpannerBatchDML hand-encodes ExecuteBatchDmlRequest: repeated
// field 1, each element a message whose field 1 is the SQL string.
func marshalSpannerBatchDML(sqls ...string) []byte {
	var out []byte
	for _, sql := range sqls {
		inner := marshalSpannerSQLRequest(sql)
		if len(inner) > 127 {
			panic("spanner test statements must fit a single-byte varint length")
		}
		out = append(out, 0x0a, byte(len(inner)))
		out = append(out, inner...)
	}
	return out
}

// spannerTestDenyDelete refuses any statement the classifier read as a
// delete — the operation-rule shape the spanner lane exists to enable.
type spannerTestDenyDelete struct{}

func (spannerTestDenyDelete) Evaluate(s inspect.Statement) policy.Verdict {
	if s.Operation == inspect.OpDelete {
		return policy.Deny("no-delete", "deletes are refused by test policy")
	}
	return policy.Allow()
}

// spannerTestDenyUnknown refuses what the classifier could not read and
// records everything it evaluated — the `operations: [unknown]` rule shape
// the fail-closed branch exists for.
type spannerTestDenyUnknown struct {
	mu    sync.Mutex
	stmts []inspect.Statement
}

func (d *spannerTestDenyUnknown) Evaluate(s inspect.Statement) policy.Verdict {
	d.mu.Lock()
	d.stmts = append(d.stmts, s)
	d.mu.Unlock()
	if s.Operation == inspect.OpUnknown {
		return policy.Deny("no-unknown", "unreadable statements are refused by test policy")
	}
	return policy.Allow()
}

func (d *spannerTestDenyUnknown) snapshot() []inspect.Statement {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]inspect.Statement(nil), d.stmts...)
}

// spannerTestRecorder allows everything and keeps what it evaluated, so a
// test can assert the exact statement shape policy saw.
type spannerTestRecorder struct {
	mu    sync.Mutex
	stmts []inspect.Statement
}

func (r *spannerTestRecorder) Evaluate(s inspect.Statement) policy.Verdict {
	r.mu.Lock()
	r.stmts = append(r.stmts, s)
	r.mu.Unlock()
	return policy.Allow()
}

func (r *spannerTestRecorder) snapshot() []inspect.Statement {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]inspect.Statement(nil), r.stmts...)
}
