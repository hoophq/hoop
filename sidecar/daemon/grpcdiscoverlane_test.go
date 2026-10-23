package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// A fail-closed discovering lane listens before it has a schema, refuses
// every RPC with UNAVAILABLE and records why, then serves with the schema
// on the same address once discovery succeeds.
func TestADiscoveringLaneRefusesUntilTheSchemaLoadsThenServes(t *testing.T) {
	upstreamAddr, upstreamCalls, stopUpstream := startDiscoverTestUpstream(t)
	defer stopUpstream()

	var reflectionUp atomic.Bool
	sink := newGRPCTestMemorySink()
	server := buildDiscoverTestServer(t, upstreamAddr,
		&GRPCCodecConfig{Discover: &GRPCDiscoverConfig{From: "reflection"}, CapturePayload: true},
		sink, func(context.Context) ([]byte, error) {
			if !reflectionUp.Load() {
				return nil, errors.New("connection refused")
			}
			return grpcTestDescriptorBytes(t), nil
		})
	addr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	// Ready once the first attempt failed: the lane answers, and one
	// upstream's failure must not take the process out of rotation.
	waitForDiscoverTestReady(t, server)
	status, message := callDiscoverTestLane(t, addr)
	if status != "14" || !strings.Contains(message, "schema discovery failed: connection refused") {
		t.Fatalf("refusal = %s %q, want UNAVAILABLE naming the failure", status, message)
	}
	if n := upstreamCalls.Load(); n != 0 {
		t.Fatalf("the upstream received %d call(s) while the lane had no schema", n)
	}
	if !hasDiscoverTestActivity(sink.snapshot(), "rpc_refused", "/test.v1.Echo/Say") {
		t.Fatal("the refusal left no rpc_refused record")
	}

	reflectionUp.Store(true)
	waitForDiscoverTestSchema(t, server)
	waitForGRPCTestAddress(t, server, nil)
	if got := server.Addr().String(); got != addr {
		t.Fatalf("the lane moved from %s to %s when the schema loaded", addr, got)
	}

	status, _ = callDiscoverTestLane(t, addr)
	if status != "0" {
		t.Fatalf("grpc-status = %s after the schema loaded, want 0", status)
	}
	if n := upstreamCalls.Load(); n != 1 {
		t.Fatalf("upstream calls = %d, want 1", n)
	}
	if !hasDiscoverTestMessage(sink.snapshot()) {
		t.Fatal("the schema-backed lane captured no message")
	}
	if _, total, _ := server.Stats(); total != 2 {
		t.Fatalf("total RPCs = %d across the swap, want 2", total)
	}
}

// A fail-open lane forwards while discovery fails, and marks what it audits
// so the trail says why no message was captured.
func TestAFailOpenDiscoveringLaneForwardsWithMethodLevelAuditOnly(t *testing.T) {
	upstreamAddr, upstreamCalls, stopUpstream := startDiscoverTestUpstream(t)
	defer stopUpstream()

	sink := newGRPCTestMemorySink()
	server := buildDiscoverTestServer(t, upstreamAddr,
		&GRPCCodecConfig{Discover: &GRPCDiscoverConfig{From: "reflection", FailOpen: true}, CapturePayload: true},
		sink, func(context.Context) ([]byte, error) {
			return nil, errors.New("reflection is not enabled")
		})
	addr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	waitForDiscoverTestReady(t, server)
	status, _ := callDiscoverTestLane(t, addr)
	if status != "0" {
		t.Fatalf("grpc-status = %s, want the upstream's 0", status)
	}
	if n := upstreamCalls.Load(); n != 1 {
		t.Fatalf("upstream calls = %d, want 1", n)
	}
	select {
	case <-sink.ended:
	case <-time.After(time.Second):
		t.Fatal("session end was not audited")
	}
	events := sink.snapshot()
	if hasDiscoverTestMessage(events) {
		t.Fatal("a lane without a schema captured a message")
	}
	var marked int
	for _, e := range events {
		if e.Kind == audit.KindStatement {
			if e.Metadata[metadataGRPCSchema] != "unavailable" {
				t.Fatalf("statement %q is not marked schema-unavailable", e.Statement)
			}
			marked++
		}
	}
	if marked == 0 {
		t.Fatal("no statement was audited")
	}
}

// A pinned lane refuses a schema whose digest differs, and says which.
func TestAPinnedDiscoveringLaneRefusesAChangedSchema(t *testing.T) {
	upstreamAddr, upstreamCalls, stopUpstream := startDiscoverTestUpstream(t)
	defer stopUpstream()

	pin := grpcSchemaDigest(grpcTestDescriptorBytes(t))
	changed, err := base64.StdEncoding.DecodeString(grpcTestLedgerDescriptorSet)
	if err != nil {
		t.Fatal(err)
	}
	server := buildDiscoverTestServer(t, upstreamAddr,
		&GRPCCodecConfig{Discover: &GRPCDiscoverConfig{From: "reflection", SHA256: pin}, CapturePayload: true},
		newGRPCTestMemorySink(), func(context.Context) ([]byte, error) { return changed, nil })
	addr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()
	waitForDiscoverTestReady(t, server)

	status, message := callDiscoverTestLane(t, addr)
	if status != "14" || !strings.Contains(message, grpcSchemaDigest(changed)) || !strings.Contains(message, pin) {
		t.Fatalf("refusal = %s %q, want UNAVAILABLE naming both digests", status, message)
	}
	if n := upstreamCalls.Load(); n != 0 {
		t.Fatalf("the upstream received %d call(s) under an unpinned schema", n)
	}
}

// The pin matching serves the schema.
func TestAPinnedDiscoveringLaneServesTheMatchingSchema(t *testing.T) {
	upstreamAddr, _, stopUpstream := startDiscoverTestUpstream(t)
	defer stopUpstream()

	blob := grpcTestDescriptorBytes(t)
	server := buildDiscoverTestServer(t, upstreamAddr,
		&GRPCCodecConfig{Discover: &GRPCDiscoverConfig{From: "reflection", SHA256: strings.ToUpper(grpcSchemaDigest(blob))}},
		newGRPCTestMemorySink(), func(context.Context) ([]byte, error) { return blob, nil })
	_, stopLane := startGRPCTestServer(t, server)
	defer stopLane()
	waitForDiscoverTestSchema(t, server)

	if status, _ := callDiscoverTestLane(t, server.Addr().String()); status != "0" {
		t.Fatalf("grpc-status = %s, want 0", status)
	}
}

// An upstream that stalls the first attempt does not leave the address
// closed: the stand-in answers while the attempt runs, and the lane is not
// ready until the attempt ends.
func TestAStalledFirstDiscoveryStillAnswersOnTheAddress(t *testing.T) {
	upstreamAddr, upstreamCalls, stopUpstream := startDiscoverTestUpstream(t)
	defer stopUpstream()

	release := make(chan struct{})
	server := buildDiscoverTestServer(t, upstreamAddr,
		&GRPCCodecConfig{Discover: &GRPCDiscoverConfig{From: "reflection"}, CapturePayload: true},
		newGRPCTestMemorySink(), func(ctx context.Context) ([]byte, error) {
			select {
			case <-release:
				return nil, errors.New("reflection is not enabled")
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
	addr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	status, message := callDiscoverTestLane(t, addr)
	if status != "14" || !strings.Contains(message, "no schema loaded yet") {
		t.Fatalf("refusal = %s %q during the first attempt, want UNAVAILABLE", status, message)
	}
	if n := upstreamCalls.Load(); n != 0 {
		t.Fatalf("the upstream received %d call(s) before a schema loaded", n)
	}
	if server.Ready() {
		t.Fatal("the lane reports ready before its first attempt ended")
	}
	close(release)
	waitForDiscoverTestReady(t, server)
}

func TestGRPCDiscoverConfigValidation(t *testing.T) {
	for name, tc := range map[string]struct {
		grpc string
		mask string
		want string
	}{
		"descriptors too":     {grpc: `"discover":{"from":"reflection"},"descriptors":"a.pb"`, want: "both name the schema"},
		"unknown source":      {grpc: `"discover":{"from":"registry"}`, want: `the only source is "reflection"`},
		"bad digest":          {grpc: `"discover":{"from":"reflection","sha256":"abc"}`, want: "not a hex SHA-256"},
		"fail open strict":    {grpc: `"discover":{"from":"reflection","fail_open":true},"strict":true`, want: "keep one"},
		"fail open masking":   {grpc: `"discover":{"from":"reflection","fail_open":true}`, mask: `,"mask":{"rules":[{"name":"m","entities":["EMAIL_ADDRESS"]}]}`, want: "cannot mask it"},
		"capture with no set": {grpc: `"capture_payload":true`, want: "needs grpc.descriptors, grpc.discover or grpc.raw"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadConfigBytes([]byte(`{"listeners":[{"name":"rpc","protocol":"grpc",` +
				`"listen":":1","upstream":"h:1","grpc":{` + tc.grpc + `}` + tc.mask + `}]}`))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}

	// An empty block is reflection: the control plane's form drops a value
	// equal to the schema default.
	ok := `{"listeners":[{"name":"rpc","protocol":"grpc","listen":":1","upstream":"h:1",` +
		`"grpc":{"discover":{"sha256":"` + strings.Repeat("a", 64) + `"},"capture_payload":true,"strict":true},` +
		`"mask":{"rules":[{"name":"m","entities":["EMAIL_ADDRESS"]}]}}]}`
	if _, err := LoadConfigBytes([]byte(ok)); err != nil {
		t.Fatalf("a fail-closed discovering lane with capture, strict and masking was refused: %v", err)
	}
}

// -validate builds the lane without dialing: the notes say what the lane
// will do, and nothing was fetched.
func TestADiscoveringLaneBuildsWithoutDialing(t *testing.T) {
	server := buildGRPCTestServer(t, "validate", "127.0.0.1:1",
		&GRPCCodecConfig{Discover: &GRPCDiscoverConfig{From: "reflection"}}, nil, nil, nil)
	notes := strings.Join(server.Notes(), "\n")
	for _, want := range []string{"over gRPC server reflection", "not pinned", "refuses every RPC"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes do not mention %q:\n%s", want, notes)
		}
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("Serve after Close = %v, want nil", err)
	}
}

type discoverTestServer interface {
	GRPCServer
	Ready() bool
}

func buildDiscoverTestServer(t *testing.T, upstream string, gc *GRPCCodecConfig, sink audit.Sink,
	fetch func(context.Context) ([]byte, error)) discoverTestServer {
	t.Helper()
	server := buildGRPCTestServer(t, "discover-test", upstream, gc, nil, nil, sink)
	d, ok := server.(*discoveringGRPCServer)
	if !ok {
		t.Fatalf("a lane with grpc.discover built %T", server)
	}
	d.fetch = fetch
	d.retry = 5 * time.Millisecond
	return d
}

func grpcTestDescriptorBytes(t *testing.T) []byte {
	t.Helper()
	blob, err := base64.StdEncoding.DecodeString(grpcTestDescriptorSet)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func startDiscoverTestUpstream(t *testing.T) (string, *atomic.Int64, func()) {
	t.Helper()
	var calls atomic.Int64
	response := grpcTestFrame(0, marshalGRPCTestMessage("s", "response"))
	addr, stop := startGRPCTestH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(response)
		w.Header().Set("Grpc-Status", "0")
	}))
	return addr, &calls, stop
}

// callDiscoverTestLane makes one Echo/Say call and returns its grpc-status
// and decoded grpc-message.
func callDiscoverTestLane(t *testing.T, addr string) (string, string) {
	t.Helper()
	transport := grpcTestTransport()
	defer transport.CloseIdleConnections()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/test.v1.Echo/Say",
		bytes.NewReader(grpcTestFrame(0, marshalGRPCTestMessage("c", "request"))))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("Te", "trailers")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	status, message := resp.Trailer.Get("Grpc-Status"), resp.Trailer.Get("Grpc-Message")
	if status == "" {
		// A trailers-only response carries the status in the headers.
		status, message = resp.Header.Get("Grpc-Status"), resp.Header.Get("Grpc-Message")
	}
	if decoded, err := url.PathUnescape(message); err == nil {
		message = decoded
	}
	return status, message
}

func waitForDiscoverTestReady(t *testing.T, server discoverTestServer) {
	t.Helper()
	waitForDiscoverTest(t, "the lane did not become ready", server.Ready)
}

// waitForDiscoverTestSchema waits until the schema-backed server is bound:
// Ready stays true across a failing lane, so it cannot tell the two apart.
func waitForDiscoverTestSchema(t *testing.T, server discoverTestServer) {
	t.Helper()
	waitForDiscoverTest(t, "the lane did not load its schema", func() bool {
		return server.Ready() && strings.Contains(strings.Join(server.Notes(), "\n"), "/test.v1.Echo/Say")
	})
}

func waitForDiscoverTest(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(time.Millisecond)
	}
}

func hasDiscoverTestActivity(events []audit.Event, activity, method string) bool {
	for _, e := range events {
		if e.Kind == audit.KindActivity && e.Metadata[audit.MetadataActivity] == activity &&
			e.Metadata["method"] == method {
			return true
		}
	}
	return false
}

func hasDiscoverTestMessage(events []audit.Event) bool {
	for _, e := range events {
		if e.Kind == audit.KindStatement && e.Metadata[inspect.MetadataGRPCMessageIndex] != "" {
			return true
		}
	}
	return false
}
