package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	codecgrpc "github.com/hoophq/libhoop/v2/codec/grpc"
)

func TestGRPCServerCapturesMasksPreservesTrailersAndAudits(t *testing.T) {
	descriptorPath := writeGRPCTestDescriptors(t)
	requestWire := marshalGRPCTestMessage("client-secret", "request")
	responseWire := marshalGRPCTestMessage("server-secret", "response")
	upstreamRequests := make(chan []byte, 1)
	upstreamAddr, stopUpstream := startGRPCTestH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("upstream protocol = %s, want HTTP/2", r.Proto)
		}
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read request: %v", readErr)
			return
		}
		upstreamRequests <- body
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(grpcTestFrame(0, responseWire))
		w.Header().Set("Grpc-Status", "0")
	}))
	defer stopUpstream()

	sink := newGRPCTestMemorySink()
	server := buildGRPCTestServer(t, "grpc-test", upstreamAddr,
		&GRPCCodecConfig{Descriptors: descriptorPath, CapturePayload: true},
		nil, grpcTestSecretMasker{}, sink)
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	transport := grpcTestTransport()
	defer transport.CloseIdleConnections()
	req, err := http.NewRequest(http.MethodPost, "http://"+laneAddr+"/test.v1.Echo/Say",
		bytes.NewReader(grpcTestFrame(0, requestWire)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("Te", "trailers")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Trailer.Get("Grpc-Status"); got != "0" {
		t.Fatalf("grpc-status = %q, want 0", got)
	}

	gotSecret, gotVisible := parseGRPCTestMessage(t, onlyGRPCTestFramePayload(t, body))
	if gotSecret != "[redacted]" {
		t.Fatalf("masked secret = %q", gotSecret)
	}
	if gotVisible != "response" {
		t.Fatalf("visible = %q", gotVisible)
	}

	select {
	case got := <-upstreamRequests:
		if !bytes.Equal(got, grpcTestFrame(0, requestWire)) {
			t.Fatalf("upstream request changed: %x", got)
		}
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive request")
	}

	select {
	case <-sink.ended:
	case <-time.After(time.Second):
		t.Fatal("session end was not audited")
	}
	events := sink.snapshot()
	var statements, masked int
	for _, event := range events {
		switch event.Kind {
		case audit.KindStatement:
			statements++
		case audit.KindMasked:
			masked++
			if event.MaskedCount != 1 || len(event.MaskedEntities) != 1 || event.MaskedEntities[0] != "secret" {
				t.Fatalf("masked event = %#v", event)
			}
		}
	}
	if statements != 4 {
		t.Fatalf("statement events = %d, want request, request message, response message, trailer", statements)
	}
	if masked != 1 {
		t.Fatalf("masked events = %d, want 1", masked)
	}
}

func TestGRPCServerDeniesRequestHeadersBeforeUpstream(t *testing.T) {
	upstreamCalled := make(chan struct{}, 1)
	upstreamAddr, stopUpstream := startGRPCTestH2C(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		upstreamCalled <- struct{}{}
	}))
	defer stopUpstream()

	server := buildGRPCTestServer(t, "deny-test", upstreamAddr, nil, grpcTestDenyAll{}, nil, nil)
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	transport := grpcTestTransport()
	defer transport.CloseIdleConnections()
	req, err := http.NewRequest(http.MethodPost, "http://"+laneAddr+"/test.v1.Echo/Say",
		bytes.NewReader(grpcTestFrame(0, nil)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if got := resp.Header.Get("Grpc-Status"); got != "7" {
		t.Fatalf("grpc-status = %q, want 7", got)
	}
	if got := codecgrpc.DecodeMessage(resp.Header.Get("Grpc-Message")); got != "blocked by test" {
		t.Fatalf("grpc-message = %q", got)
	}
	select {
	case <-upstreamCalled:
		t.Fatal("denied RPC reached upstream")
	default:
	}
	active, total, denied := server.Stats()
	if active != 0 || total != 1 || denied != 1 {
		t.Fatalf("stats = (%d, %d, %d), want (0, 1, 1)", active, total, denied)
	}
}

func TestGRPCServerWithholdsDeniedRequestMessage(t *testing.T) {
	descriptorPath := writeGRPCTestDescriptors(t)
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

	server := buildGRPCTestServer(t, "request-deny-test", upstreamAddr,
		&GRPCCodecConfig{Descriptors: descriptorPath, CapturePayload: true},
		grpcTestDenyClientMessages{}, nil, nil)
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	transport := grpcTestTransport()
	defer transport.CloseIdleConnections()
	req, err := http.NewRequest(http.MethodPost, "http://"+laneAddr+"/test.v1.Echo/Say",
		bytes.NewReader(grpcTestFrame(0,
			marshalGRPCTestMessage("forbidden", "request"))))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if got := resp.Header.Get("Grpc-Status"); got != "7" {
		t.Fatalf("grpc-status = %q, want 7", got)
	}
	if got := codecgrpc.DecodeMessage(resp.Header.Get("Grpc-Message")); got != "request blocked by test" {
		t.Fatalf("grpc-message = %q", got)
	}
	select {
	case got := <-upstreamReads:
		if len(got.body) != 0 {
			t.Fatalf("denied request bytes reached upstream: %x", got.body)
		}
		if got.err == nil {
			t.Fatal("upstream saw a clean EOF for a denied request")
		}
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe the refused request stream")
	}
}

func TestGRPCServerResponseMessageDenialIsScopedToStream(t *testing.T) {
	descriptorPath := writeGRPCTestDescriptors(t)
	forbiddenWire := marshalGRPCTestMessage("forbidden", "first")
	allowedWire := marshalGRPCTestMessage("allowed", "second")
	var upstreamCalls atomic.Int32
	upstreamPeers := make(chan string, 2)
	upstreamAddr, stopUpstream := startGRPCTestH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPeers <- r.RemoteAddr
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message, Grpc-Status-Details-Bin")
		w.WriteHeader(http.StatusOK)
		wire := forbiddenWire
		if upstreamCalls.Add(1) > 1 {
			wire = allowedWire
		}
		_, _ = w.Write(grpcTestFrame(0, wire))
		w.Header().Set("Grpc-Status", "0")
		w.Header().Set("Grpc-Status-Details-Bin", "stale-upstream-details")
	}))
	defer stopUpstream()

	server := buildGRPCTestServer(t, "response-deny-test", upstreamAddr,
		&GRPCCodecConfig{Descriptors: descriptorPath, CapturePayload: true},
		grpcTestDenyForbiddenServerMessage{}, nil, nil)
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	transport := grpcTestTransport()
	defer transport.CloseIdleConnections()
	doRPC := func(secret string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, "http://"+laneAddr+"/test.v1.Echo/Say",
			bytes.NewReader(grpcTestFrame(0,
				marshalGRPCTestMessage(secret, "request"))))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/grpc")
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return resp, body
	}

	deniedResp, deniedBody := doRPC("first")
	if len(deniedBody) != 0 {
		t.Fatalf("denied response body reached client: %x", deniedBody)
	}
	if got := deniedResp.Trailer.Get("Grpc-Status"); got != "7" {
		t.Fatalf("grpc-status = %q, want 7", got)
	}
	if got := codecgrpc.DecodeMessage(deniedResp.Trailer.Get("Grpc-Message")); got != "response blocked by test" {
		t.Fatalf("grpc-message = %q", got)
	}
	if got := deniedResp.Trailer.Get("Grpc-Status-Details-Bin"); got != "" {
		t.Fatalf("grpc-status-details-bin = %q, want removed on denial", got)
	}

	allowedResp, allowedBody := doRPC("second")
	if got := allowedResp.Trailer.Get("Grpc-Status"); got != "0" {
		t.Fatalf("second grpc-status = %q, want 0", got)
	}
	_, allowedVisible := parseGRPCTestMessage(t, onlyGRPCTestFramePayload(t, allowedBody))
	if allowedVisible != "second" {
		t.Fatalf("second visible = %q", allowedVisible)
	}
	firstPeer, secondPeer := <-upstreamPeers, <-upstreamPeers
	if secondPeer != firstPeer {
		t.Fatalf("denial closed HTTP/2 connection: first upstream peer %q, second %q", firstPeer, secondPeer)
	}

	active, total, denied := server.Stats()
	if active != 0 || total != 2 || denied != 1 {
		t.Fatalf("stats = (%d, %d, %d), want (0, 2, 1)", active, total, denied)
	}
}

func TestGRPCServerServePublishesAddressStatsAndStopsWithContext(t *testing.T) {
	server := buildGRPCTestServer(t, "lifecycle-test", "127.0.0.1:1", nil, nil, nil, nil)
	active, total, denied := server.Stats()
	if active != 0 || total != 0 || denied != 0 {
		t.Fatalf("initial stats = (%d, %d, %d), want (0, 0, 0)", active, total, denied)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()

	waitForGRPCTestAddress(t, server, done)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not stop after cancellation")
	}
}

func TestGRPCValidationLoadsDescriptorAndReportsPaths(t *testing.T) {
	descriptorPath := writeGRPCTestDescriptors(t)
	raw, err := json.Marshal(map[string]any{
		"listeners": []any{map[string]any{
			"name": "validate-grpc", "protocol": "grpc",
			"listen": "127.0.0.1:0", "upstream": "127.0.0.1:1",
			"grpc": map[string]any{"descriptors": descriptorPath},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	lanes, err := Validate(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(lanes) != 1 || len(lanes[0].Notes) == 0 {
		t.Fatalf("validation report = %#v, want descriptor method notes", lanes)
	}
	if !strings.Contains(lanes[0].Notes[0], "/test.v1.Echo/Say") {
		t.Fatalf("descriptor note = %q", lanes[0].Notes[0])
	}
}

func TestGRPCServerRejectsNonPOSTBeforeUpstream(t *testing.T) {
	upstreamCalled := make(chan struct{}, 1)
	upstreamAddr, stopUpstream := startGRPCTestH2C(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		upstreamCalled <- struct{}{}
	}))
	defer stopUpstream()

	server := buildGRPCTestServer(t, "method-test", upstreamAddr, nil, nil, nil, nil)
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	transport := grpcTestTransport()
	defer transport.CloseIdleConnections()
	req, err := http.NewRequest(http.MethodGet, "http://"+laneAddr+"/test.v1.Echo/Say", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if got := resp.Header.Get("Grpc-Status"); got != "12" {
		t.Fatalf("grpc-status = %q, want 12", got)
	}
	select {
	case <-upstreamCalled:
		t.Fatal("non-POST request reached upstream")
	default:
	}
}

func TestGRPCServerInspectsBodyWhenStatusIsInInitialHeaders(t *testing.T) {
	descriptorPath := writeGRPCTestDescriptors(t)
	responseWire := marshalGRPCTestMessage("server-secret", "response")
	upstreamAddr, stopUpstream := startGRPCTestH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Grpc-Status", "0")
		_, _ = w.Write(grpcTestFrame(0, responseWire))
	}))
	defer stopUpstream()

	server := buildGRPCTestServer(t, "header-status-test", upstreamAddr,
		&GRPCCodecConfig{Descriptors: descriptorPath}, nil, grpcTestSecretMasker{}, nil)
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	transport := grpcTestTransport()
	defer transport.CloseIdleConnections()
	req, err := http.NewRequest(http.MethodPost, "http://"+laneAddr+"/test.v1.Echo/Say",
		bytes.NewReader(grpcTestFrame(0,
			marshalGRPCTestMessage("request", "request"))))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	maskedSecret, _ := parseGRPCTestMessage(t, onlyGRPCTestFramePayload(t, body))
	if maskedSecret != "[redacted]" {
		t.Fatalf("masked secret = %q", maskedSecret)
	}
}

func TestGRPCServerAuditsLocalDenialAsServerTrailer(t *testing.T) {
	upstreamAddr, stopUpstream := startGRPCTestH2C(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer stopUpstream()
	sink := newGRPCTestMemorySink()
	server := buildGRPCTestServer(t, "local-status-test", upstreamAddr, nil, grpcTestDenyAll{}, nil, sink)
	laneAddr, stopLane := startGRPCTestServer(t, server)
	defer stopLane()

	transport := grpcTestTransport()
	defer transport.CloseIdleConnections()
	req, err := http.NewRequest(http.MethodPost, "http://"+laneAddr+"/test.v1.Echo/Say",
		bytes.NewReader(grpcTestFrame(0, nil)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	select {
	case <-sink.ended:
	case <-time.After(time.Second):
		t.Fatal("session end was not audited")
	}
	for _, event := range sink.snapshot() {
		if (event.Kind == audit.KindStatement || event.Kind == audit.KindViolation) &&
			event.Direction == inspect.FromServer &&
			event.Metadata[inspect.MetadataGRPCStatusCode] == "7" {
			return
		}
	}
	t.Fatal("local PermissionDenied trailer statement was not audited")
}

func buildGRPCTestServer(
	t *testing.T,
	name string,
	upstream string,
	grpcConfig *GRPCCodecConfig,
	evaluator policy.Evaluator,
	masker gate.Masker,
	sink audit.Sink,
) GRPCServer {
	t.Helper()
	server, err := buildGRPCServer(lane{
		cfg: ListenerConfig{
			Name:     name,
			Protocol: "grpc",
			Listen:   "127.0.0.1:0",
			Upstream: upstream,
			GRPC:     grpcConfig,
		},
		name:   name,
		policy: evaluator,
		masker: masker,
	}, AuditConfig{}, sink, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func startGRPCTestServer(t *testing.T, server GRPCServer) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	waitForGRPCTestAddress(t, server, done)
	return server.Addr().String(), func() {
		cancel()
		_ = server.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("gRPC server stopped with error: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("gRPC server did not stop")
		}
	}
}

func waitForGRPCTestAddress(t *testing.T, server GRPCServer, done <-chan error) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for server.Addr() == nil {
		select {
		case err := <-done:
			t.Fatalf("Serve returned before binding: %v", err)
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("server did not publish its bound address")
		}
	}
}

func startGRPCTestH2C(t *testing.T, handler http.Handler) (string, func()) {
	t.Helper()
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	return startGRPCTestHTTPServer(t, &http.Server{Handler: handler, Protocols: protocols})
}

func startGRPCTestHTTPServer(t *testing.T, server *http.Server) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(listener)
	}()
	return listener.Addr().String(), func() {
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("HTTP server did not stop")
		}
	}
}

func grpcTestTransport() *http.Transport {
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Transport{Protocols: protocols, DisableCompression: true}
}

// grpcTestDescriptorSet is a serialized google.protobuf.FileDescriptorSet:
// test.v1.Echo/Say, with Request and Response messages both carrying
// secret=1 and visible=2 proto3 string fields. Checked in as bytes so this
// package needs no protobuf dependency; regenerate by marshalling a
// descriptorpb.FileDescriptorSet of that shape.
const grpcTestDescriptorSet = "CqoBCgplY2hvLnByb3RvEgd0ZXN0LnYxIioKB1JlcXVlc3QSDgoGc2VjcmV0GAEgASgJEg8KB3Zpc2libGUYAiABKAkiKwoIUmVzcG9uc2USDgoGc2VjcmV0GAEgASgJEg8KB3Zpc2libGUYAiABKAkyMgoERWNobxIqCgNTYXkSEC50ZXN0LnYxLlJlcXVlc3QaES50ZXN0LnYxLlJlc3BvbnNlYgZwcm90bzM="

func writeGRPCTestDescriptors(t *testing.T) string {
	t.Helper()
	blob, err := base64.StdEncoding.DecodeString(grpcTestDescriptorSet)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/descriptors.pb"
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	schema, err := codecgrpc.LoadSchema(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := schema.Lookup("/test.v1.Echo/Say"); !ok {
		t.Fatal("fixture method missing")
	}
	return path
}

// marshalGRPCTestMessage hand-encodes the {secret=1, visible=2} string
// fields. Values must fit a single-byte varint length.
func marshalGRPCTestMessage(secret, visible string) []byte {
	if len(secret) > 127 || len(visible) > 127 {
		panic("grpc test values must fit a single-byte varint length")
	}
	out := make([]byte, 0, 4+len(secret)+len(visible))
	out = append(out, 0x0a, byte(len(secret)))
	out = append(out, secret...)
	out = append(out, 0x12, byte(len(visible)))
	out = append(out, visible...)
	return out
}

// parseGRPCTestMessage decodes the same two-string-field shape from standard
// protobuf wire format, tolerating any field order.
func parseGRPCTestMessage(t *testing.T, payload []byte) (secret, visible string) {
	t.Helper()
	for i := 0; i < len(payload); {
		tag, n := binary.Uvarint(payload[i:])
		if n <= 0 {
			t.Fatalf("bad tag varint at %d in %x", i, payload)
		}
		i += n
		if tag&7 != 2 {
			t.Fatalf("unexpected wire type %d in %x", tag&7, payload)
		}
		size, n := binary.Uvarint(payload[i:])
		if n <= 0 || i+n+int(size) > len(payload) {
			t.Fatalf("bad length at %d in %x", i, payload)
		}
		i += n
		value := string(payload[i : i+int(size)])
		i += int(size)
		switch tag >> 3 {
		case 1:
			secret = value
		case 2:
			visible = value
		default:
			t.Fatalf("unexpected field %d in %x", tag>>3, payload)
		}
	}
	return secret, visible
}

func grpcTestFrame(compressed byte, payload []byte) []byte {
	out := make([]byte, 5, 5+len(payload))
	out[0] = compressed
	binary.BigEndian.PutUint32(out[1:], uint32(len(payload)))
	return append(out, payload...)
}

func onlyGRPCTestFramePayload(t *testing.T, wire []byte) []byte {
	t.Helper()
	if len(wire) < 5 {
		t.Fatalf("short frame: %x", wire)
	}
	size := int(binary.BigEndian.Uint32(wire[1:5]))
	if len(wire) != 5+size {
		t.Fatalf("frame length = %d, body bytes = %d", size, len(wire)-5)
	}
	return wire[5:]
}

type grpcTestSecretMasker struct{}

func (grpcTestSecretMasker) Mask(data []byte) ([]byte, []string, int) {
	return data, nil, 0
}

func (grpcTestSecretMasker) MaskCell(column string, value []byte) ([]byte, []string, int) {
	if column == "secret" {
		return []byte("[redacted]"), []string{"secret"}, 1
	}
	return value, nil, 0
}

type grpcTestDenyAll struct{}

func (grpcTestDenyAll) Evaluate(inspect.Statement) policy.Verdict {
	return policy.Deny("test-deny", "blocked by test")
}

type grpcTestDenyClientMessages struct{}

func (grpcTestDenyClientMessages) Evaluate(statement inspect.Statement) policy.Verdict {
	if statement.Direction == inspect.FromClient && statement.HTTP != nil && statement.HTTP.Body != "" {
		return policy.Deny("request-test-deny", "request blocked by test")
	}
	return policy.Allow()
}

type grpcTestDenyForbiddenServerMessage struct{}

func (grpcTestDenyForbiddenServerMessage) Evaluate(statement inspect.Statement) policy.Verdict {
	if statement.Direction == inspect.FromServer && statement.HTTP != nil &&
		strings.Contains(statement.HTTP.Body, "forbidden") {
		return policy.Deny("response-test-deny", "response blocked by test")
	}
	return policy.Allow()
}

type grpcTestMemorySink struct {
	mu     sync.Mutex
	events []audit.Event
	ended  chan struct{}
	once   sync.Once
}

func newGRPCTestMemorySink() *grpcTestMemorySink {
	return &grpcTestMemorySink{ended: make(chan struct{})}
}

func (s *grpcTestMemorySink) Write(_ context.Context, event audit.Event) error {
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	if event.Kind == audit.KindSessionEnd {
		s.once.Do(func() { close(s.ended) })
	}
	return nil
}

func (s *grpcTestMemorySink) Close() error { return nil }

func (s *grpcTestMemorySink) snapshot() []audit.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]audit.Event(nil), s.events...)
}
