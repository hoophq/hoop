package daemon

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// A raw lane has no schema. It renders both directions by field number for
// policy and audit, masks the declared response path, and records the
// masked value, never the clear one.
func TestARawLaneRendersByFieldNumberAndMasksDeclaredPaths(t *testing.T) {
	upstreamAddr, stopUpstream := startRawTestUpstream(t, marshalGRPCTestMessage("server-secret", "response"))
	defer stopUpstream()

	sink := newGRPCTestMemorySink()
	server := buildGRPCTestServer(t, "raw-test", upstreamAddr, &GRPCCodecConfig{
		Raw: &GRPCRawConfig{StringFields: []GRPCRawStringFields{
			{Method: "/test.v1.Echo/Say", Response: []string{"1"}},
		}},
		CapturePayload: true,
	}, nil, rawTestFieldMasker{path: "1"}, sink)
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	status, body := callRawTestLane(t, laneAddr, marshalGRPCTestMessage("client-secret", "request"))
	if status != "0" {
		t.Fatalf("grpc-status = %s, want 0", status)
	}
	secret, visible := parseGRPCTestMessage(t, onlyGRPCTestFramePayload(t, body))
	if secret != "[redacted]" || visible != "response" {
		t.Fatalf("client got secret=%q visible=%q, want field 1 masked and field 2 untouched", secret, visible)
	}

	select {
	case <-sink.ended:
	case <-time.After(time.Second):
		t.Fatal("session end was not audited")
	}
	var request, response bool
	for _, e := range sink.snapshot() {
		if e.Kind != audit.KindStatement || e.Metadata[inspect.MetadataGRPCMessageIndex] == "" {
			continue
		}
		if e.Metadata[metadataGRPCSchema] != "raw" {
			t.Errorf("statement %q is not marked grpc.schema: raw", e.Statement)
		}
		switch e.Direction {
		case inspect.FromClient:
			request = strings.Contains(e.Statement, `{"1":"client-secret","2":"request"}`)
		case inspect.FromServer:
			if strings.Contains(e.Statement, "server-secret") {
				t.Fatalf("the audit trail carries the clear value: %q", e.Statement)
			}
			response = strings.Contains(e.Statement, `{"1":"[redacted]","2":"response"}`)
		}
	}
	if !request || !response {
		t.Fatalf("raw renderings missing: request=%v response=%v", request, response)
	}
}

// Rules read the raw rendering: a request whose field carries a value a
// rule refuses is denied, and the message never reaches the upstream.
func TestARawLaneEvaluatesRulesOnRequestFields(t *testing.T) {
	upstreamAddr, received, stopUpstream := startCountingRawTestUpstream(t, marshalGRPCTestMessage("s", "v"))
	defer stopUpstream()

	server := buildGRPCTestServer(t, "raw-deny", upstreamAddr,
		&GRPCCodecConfig{Raw: &GRPCRawConfig{}, CapturePayload: true},
		rawTestDenyBody{needle: "111.444.777-35"}, nil, nil)
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	status, _ := callRawTestLane(t, laneAddr, marshalGRPCTestMessage("cpf 111.444.777-35", "x"))
	if status != "7" {
		t.Fatalf("grpc-status = %s, want PERMISSION_DENIED", status)
	}
	// The headers reach the upstream before the message is read, as on
	// every grpc lane; the denied frame must not.
	if got := received(); strings.Contains(got, "111.444.777-35") {
		t.Fatalf("the upstream received the denied message: %q", got)
	}
}

// Bytes that contradict a declaration refuse the response: the lane does
// not guess what to rewrite.
func TestARawLaneRefusesAResponseThatContradictsTheDeclaration(t *testing.T) {
	notAString := []byte{0x08, 0x2a} // field 1 is a varint, declared a string
	upstreamAddr, stopUpstream := startRawTestUpstream(t, notAString)
	defer stopUpstream()

	server := buildGRPCTestServer(t, "raw-mismatch", upstreamAddr, &GRPCCodecConfig{
		Raw: &GRPCRawConfig{StringFields: []GRPCRawStringFields{
			{Method: "/test.v1.Echo/Say", Response: []string{"1"}},
		}},
	}, nil, rawTestFieldMasker{path: "1"}, nil)
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	status, body := callRawTestLane(t, laneAddr, marshalGRPCTestMessage("c", "r"))
	if status != "13" {
		t.Fatalf("grpc-status = %s, want INTERNAL", status)
	}
	if len(body) != 0 {
		t.Fatalf("the refused response still reached the client: %x", body)
	}
}

// A masking raw lane refuses a method it has no declaration for, before
// the upstream is dialed: forwarding it would hand the client whatever the
// rules were there to hide. A method declared with no paths forwards.
func TestAMaskingRawLaneRefusesAnUndeclaredMethod(t *testing.T) {
	for name, tc := range map[string]struct {
		declared   GRPCRawStringFields
		wantStatus string
		wantCalled bool
	}{
		"undeclared":         {declared: GRPCRawStringFields{Method: "/test.v1.Echo/Other", Response: []string{"1"}}, wantStatus: "9"},
		"declared, no paths": {declared: GRPCRawStringFields{Method: "/test.v1.Echo/Say"}, wantStatus: "0", wantCalled: true},
	} {
		t.Run(name, func(t *testing.T) {
			upstreamAddr, received, stopUpstream := startCountingRawTestUpstream(t, marshalGRPCTestMessage("server-secret", "r"))
			defer stopUpstream()
			server := buildGRPCTestServer(t, "raw-undeclared", upstreamAddr, &GRPCCodecConfig{
				Raw: &GRPCRawConfig{StringFields: []GRPCRawStringFields{tc.declared}},
			}, nil, rawTestFieldMasker{path: "1"}, nil)
			laneAddr, stopLane := startGRPCTestServer(t, server)
			defer stopLane()

			status, body := callRawTestLane(t, laneAddr, marshalGRPCTestMessage("c", "request"))
			if status != tc.wantStatus {
				t.Fatalf("grpc-status = %s, want %s", status, tc.wantStatus)
			}
			if called := received() != ""; called != tc.wantCalled {
				t.Fatalf("upstream called = %v, want %v", called, tc.wantCalled)
			}
			if !tc.wantCalled && len(body) != 0 {
				t.Fatalf("a refused RPC returned a body: %x", body)
			}
		})
	}
}

// An AI mask names entities, not fields, and a raw lane rewrites only the
// declared string fields. So a statement the AI masks is refused there:
// forwarding it would hand the client the undeclared field in the clear.
func TestARawLaneRefusesAStatementTheAIMasks(t *testing.T) {
	// Field 1 carries the secret and is not declared; field 2 is.
	upstreamAddr, stopUpstream := startRawTestUpstream(t, marshalGRPCTestMessage("server-secret", "visible"))
	defer stopUpstream()

	la := laneBlock()
	la.HighRisk = RiskLevel{Action: "warn", Mask: &AIRuleOptions{}}
	lc := ListenerConfig{
		Name: "raw-ai-mask", Protocol: "grpc", Listen: "127.0.0.1:0", Upstream: upstreamAddr,
		GRPC: &GRPCCodecConfig{Raw: &GRPCRawConfig{StringFields: []GRPCRawStringFields{
			{Method: "/test.v1.Echo/Say", Response: []string{"2"}},
		}}},
		Analyzer: la,
	}
	server, _, err := buildGRPCServer(lane{cfg: lc, name: lc.Name, policy: rawTestAIMask{},
		maskScope: laneMaskScope(lc)}, AuditConfig{}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	status, body := callRawTestLane(t, laneAddr, marshalGRPCTestMessage("c", "request"))
	if bytes.Contains(body, []byte("server-secret")) {
		t.Fatalf("the undeclared field reached the client in the clear: %q", body)
	}
	if status != "7" {
		t.Fatalf("grpc-status = %s, want PERMISSION_DENIED", status)
	}
}

func TestGRPCRawConfigValidation(t *testing.T) {
	mask := `,"mask":{"rules":[{"name":"m","entities":["EMAIL_ADDRESS"]}]}`
	for name, tc := range map[string]struct {
		protocol string
		grpc     string
		extra    string
		want     string
	}{
		"descriptors too":        {grpc: `"raw":{},"descriptors":"a.pb"`, want: "keep one"},
		"discover too":           {grpc: `"raw":{},"discover":{}`, want: "keep one"},
		"strict":                 {grpc: `"raw":{},"strict":true`, want: "grpc.strict needs a schema"},
		"bad path":               {grpc: `"raw":{"string_fields":[{"method":"/a.B/C","response":["x"]}]}`, want: `response path "x"`},
		"bad method":             {grpc: `"raw":{"string_fields":[{"method":"C","response":["1"]}]}`, want: "not a /package.Service/Method path"},
		"duplicate":              {grpc: `"raw":{"string_fields":[{"method":"/a.B/C","response":["1"]},{"method":"/a.B/C","response":["2"]}]}`, want: "lists \"/a.B/C\" twice"},
		"mask, nothing declared": {grpc: `"raw":{},"capture_payload":true`, extra: mask, want: "declares none"},
		"spanner":                {protocol: "spanner", grpc: `"raw":{}`, want: "not supported on a spanner listener"},
	} {
		t.Run(name, func(t *testing.T) {
			protocol := tc.protocol
			if protocol == "" {
				protocol = "grpc"
			}
			_, err := LoadConfigBytes([]byte(`{"listeners":[{"name":"rpc","protocol":"` + protocol + `",` +
				`"listen":":1","upstream":"h:1","grpc":{` + tc.grpc + `}` + tc.extra + `}]}`))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}

	ok := `{"listeners":[{"name":"rpc","protocol":"grpc","listen":":1","upstream":"h:1",` +
		`"grpc":{"raw":{"string_fields":[{"method":"/demo.v1.Ledger/GetInvoice","response":["2","4.1"]}]},"capture_payload":true}` +
		mask + `}]}`
	if _, err := LoadConfigBytes([]byte(ok)); err != nil {
		t.Fatalf("a raw lane with capture and declared masking was refused: %v", err)
	}
}

// rawTestFieldMasker masks one field-number path, the way a mask rule's
// columns: ["1"] does on a raw lane.
type rawTestFieldMasker struct{ path string }

func (rawTestFieldMasker) Mask(data []byte) ([]byte, []string, int) { return data, nil, 0 }

func (m rawTestFieldMasker) MaskCell(path string, value []byte) ([]byte, []string, int) {
	if path == m.path {
		return []byte("[redacted]"), []string{"field " + path}, 1
	}
	return value, nil, 0
}

type rawTestDenyBody struct{ needle string }

func (d rawTestDenyBody) Evaluate(s inspect.Statement) policy.Verdict {
	if s.HTTP != nil && strings.Contains(s.HTTP.Body, d.needle) {
		return policy.Deny("raw-test-deny", "refused by test")
	}
	return policy.Allow()
}

// rawTestAIMask masks the response of every client statement, as the AI
// analyzer does when it applies a mask: any value holding "secret".
type rawTestAIMask struct{}

func (rawTestAIMask) Evaluate(inspect.Statement) policy.Verdict { return policy.Allow() }

func (rawTestAIMask) EvaluateWith(s inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	if s.Direction == inspect.FromClient {
		ec.MaskResponse(rawTestSecretMask{})
	}
	return policy.Allow()
}

type rawTestSecretMask struct{}

func (rawTestSecretMask) Mask(data []byte) ([]byte, []string, int) { return data, nil, 0 }

func (rawTestSecretMask) MaskCell(_ string, value []byte) ([]byte, []string, int) {
	if bytes.Contains(value, []byte("secret")) {
		return []byte("[AI]"), []string{"SECRET"}, 1
	}
	return value, nil, 0
}

func startRawTestUpstream(t *testing.T, response []byte) (string, func()) {
	t.Helper()
	addr, _, stop := startCountingRawTestUpstream(t, response)
	return addr, stop
}

// startCountingRawTestUpstream answers every call with response and
// returns what request bodies it received, concatenated.
func startCountingRawTestUpstream(t *testing.T, response []byte) (string, func() string, func()) {
	t.Helper()
	var mu sync.Mutex
	var received bytes.Buffer
	addr, stop := startGRPCTestH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received.Write(body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(grpcTestFrame(0, response))
		w.Header().Set("Grpc-Status", "0")
	}))
	return addr, func() string { mu.Lock(); defer mu.Unlock(); return received.String() }, stop
}

func callRawTestLane(t *testing.T, addr string, message []byte) (string, []byte) {
	t.Helper()
	transport := grpcTestTransport()
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"http://"+addr+"/test.v1.Echo/Say", bytes.NewReader(grpcTestFrame(0, message)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("Te", "trailers")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	status := resp.Trailer.Get("Grpc-Status")
	if status == "" {
		status = resp.Header.Get("Grpc-Status")
	}
	return status, body
}
