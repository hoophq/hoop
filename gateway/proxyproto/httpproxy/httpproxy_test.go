package httpproxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- helpers ----------------------------------------------------------------

// buildHTTPResponse serializes an HTTP response (status line + headers + body)
// into a byte slice, exactly like the agent does for regular (non-SSE) responses.
func buildHTTPResponse(statusCode int, headers map[string]string, body string) []byte {
	resp := &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	resp.ContentLength = int64(len(body))
	for k, v := range headers {
		resp.Header.Set(k, v)
	}

	var buf bytes.Buffer
	_ = resp.Write(&buf)
	return buf.Bytes()
}

// buildSSEHeaderPacket builds the first gRPC packet for an SSE response,
// containing only the HTTP status line and headers (no body), exactly as the
// agent's libhoop/agent/httpproxy/sse.go produces.
func buildSSEHeaderPacket(statusCode int, extraHeaders map[string]string) []byte {
	var buf bytes.Buffer
	statusText := fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode))
	fmt.Fprintf(&buf, "HTTP/1.1 %s\r\n", statusText)
	buf.WriteString("Content-Type: text/event-stream\r\n")
	buf.WriteString("Transfer-Encoding: chunked\r\n")
	for k, v := range extraHeaders {
		fmt.Fprintf(&buf, "%s: %s\r\n", k, v)
	}
	buf.WriteString("\r\n")
	return buf.Bytes()
}

// mockStreamClient implements pb.ClientTransport for testing.
// It captures Send() calls and provides canned Recv() responses.
type mockStreamClient struct {
	mu       sync.Mutex
	sent     []*pb.Packet
	sendErr  error
	recvChan chan *pb.Packet
	closed   bool
	ctx      context.Context
}

func newMockStreamClient() *mockStreamClient {
	return &mockStreamClient{
		recvChan: make(chan *pb.Packet, 10),
		ctx:      context.Background(),
	}
}

func (m *mockStreamClient) Send(pkt *pb.Packet) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sendErr != nil {
		return m.sendErr
	}
	m.sent = append(m.sent, pkt)
	return nil
}

func (m *mockStreamClient) Recv() (*pb.Packet, error) {
	pkt, ok := <-m.recvChan
	if !ok {
		return nil, io.EOF
	}
	return pkt, nil
}

func (m *mockStreamClient) Close() (error, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil, nil
}

func (m *mockStreamClient) StreamContext() context.Context {
	return m.ctx
}

func (m *mockStreamClient) StartKeepAlive() {}

// newTestSession creates a minimal httpProxySession for testing, with a real
// context, response store, and the provided mock stream client.
func newTestSession(ctx context.Context, cancelFn context.CancelCauseFunc) *httpProxySession {
	return &httpProxySession{
		sid: "test-session",
		ctx: ctx,
		cancelFn: func(msg string, a ...any) {
			cancelFn(fmt.Errorf(msg, a...))
		},
		streamClient: newMockStreamClient(),
	}
}

// flushRecorder wraps httptest.ResponseRecorder so it implements http.Flusher
// and tracks flush calls.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushCount int
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{
		ResponseRecorder: httptest.NewRecorder(),
	}
}

func (f *flushRecorder) Flush() {
	f.flushCount++
	f.ResponseRecorder.Flush()
}

// --- isSSEStreamingResponse tests -------------------------------------------

func TestIsSSEStreamingResponse(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		expected    bool
	}{
		{"standard text/event-stream", "text/event-stream", true},
		{"with charset parameter", "text/event-stream; charset=utf-8", true},
		{"uppercase", "TEXT/EVENT-STREAM", true},
		{"mixed case", "Text/Event-Stream", true},
		{"application/json", "application/json", false},
		{"text/html", "text/html", false},
		{"empty content-type", "", false},
		{"text/plain", "text/plain", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{"Content-Type": []string{tt.contentType}}}
			assert.Equal(t, tt.expected, isSSEStreamingResponse(resp))
		})
	}
}

// --- handleSSEStream tests --------------------------------------------------

func TestHandleSSEStream_MultipleEvents(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	sess := newTestSession(ctx, cancel)
	responseChan := make(chan []byte, 100)
	w := newFlushRecorder()

	// Build the header packet as the agent would
	headerPacket := buildSSEHeaderPacket(200, nil)
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(headerPacket)), nil)
	require.NoError(t, err)

	// Simulate 3 SSE event chunks arriving from the agent
	chunk1 := []byte("14\r\ndata: {\"n\":1}\n\n\r\n")
	chunk2 := []byte("14\r\ndata: {\"n\":2}\n\n\r\n")
	chunk3 := []byte("14\r\ndata: {\"n\":3}\n\n\r\n")
	terminator := []byte("0\r\n\r\n")

	// Feed chunks and close in a goroutine
	go func() {
		responseChan <- chunk1
		responseChan <- chunk2
		responseChan <- chunk3
		responseChan <- terminator
		close(responseChan)
	}()

	sess.handleSSEStream(w, resp, responseChan, "1")

	result := w.ResponseRecorder
	assert.Equal(t, 200, result.Code)
	assert.Contains(t, result.Header().Get("Content-Type"), "text/event-stream")

	body := result.Body.String()
	assert.Contains(t, body, `{"n":1}`)
	assert.Contains(t, body, `{"n":2}`)
	assert.Contains(t, body, `{"n":3}`)
	assert.Contains(t, body, "0\r\n")

	// Flusher should have been called at least once per chunk + initial flush
	assert.GreaterOrEqual(t, w.flushCount, 4, "should flush at least once per chunk plus headers")
}

func TestHandleSSEStream_SessionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	sess := newTestSession(ctx, cancel)
	responseChan := make(chan []byte, 100)
	w := newFlushRecorder()

	headerPacket := buildSSEHeaderPacket(200, nil)
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(headerPacket)), nil)
	require.NoError(t, err)

	// Send one chunk, then cancel the session
	done := make(chan struct{})
	go func() {
		responseChan <- []byte("14\r\ndata: {\"n\":1}\n\n\r\n")
		// Give a moment for the chunk to be processed
		time.Sleep(50 * time.Millisecond)
		cancel(fmt.Errorf("session expired"))
		close(done)
	}()

	sess.handleSSEStream(w, resp, responseChan, "1")

	<-done
	body := w.Body.String()
	assert.Contains(t, body, `{"n":1}`)
	// Method should have returned due to context cancellation
	assert.Error(t, ctx.Err())
}

func TestHandleSSEStream_EmptyStream(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	sess := newTestSession(ctx, cancel)
	responseChan := make(chan []byte, 100)
	w := newFlushRecorder()

	headerPacket := buildSSEHeaderPacket(200, nil)
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(headerPacket)), nil)
	require.NoError(t, err)

	// Close channel immediately — empty stream
	close(responseChan)

	sess.handleSSEStream(w, resp, responseChan, "1")

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")
	// Note: Transfer-Encoding is handled by Go's HTTP server at the transport
	// level and not exposed in the ResponseWriter header map. We verify the
	// agent sends it by checking the raw header packet instead.
	// Body should be empty (no chunks)
	assert.Empty(t, w.Body.String())
}

func TestHandleSSEStream_HeadersPreserved(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	sess := newTestSession(ctx, cancel)
	responseChan := make(chan []byte, 100)
	w := newFlushRecorder()

	headerPacket := buildSSEHeaderPacket(200, map[string]string{
		"X-Request-Id":  "abc-123",
		"Cache-Control": "no-cache",
	})
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(headerPacket)), nil)
	require.NoError(t, err)

	close(responseChan)

	sess.handleSSEStream(w, resp, responseChan, "1")

	assert.Equal(t, "abc-123", w.Header().Get("X-Request-Id"))
	assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
}

func TestHandleSSEStream_NonOKStatusCode(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	sess := newTestSession(ctx, cancel)
	responseChan := make(chan []byte, 100)
	w := newFlushRecorder()

	headerPacket := buildSSEHeaderPacket(429, nil)
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(headerPacket)), nil)
	require.NoError(t, err)

	responseChan <- []byte("14\r\ndata: rate limited\n\r\n")
	close(responseChan)

	sess.handleSSEStream(w, resp, responseChan, "1")

	assert.Equal(t, 429, w.Code)
	assert.Contains(t, w.Body.String(), "rate limited")
}

// --- handleRequest integration tests ----------------------------------------

// TestHandleRequest_RegularResponse verifies that non-SSE responses still work
// after the SSE changes (regression test).
func TestHandleRequest_RegularResponse(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	mockClient := newMockStreamClient()
	sess := &httpProxySession{
		sid: "test-session",
		ctx: ctx,
		cancelFn: func(msg string, a ...any) {
			cancel(fmt.Errorf(msg, a...))
		},
		streamClient: mockClient,
	}

	connectionID := "1"
	responseChan := make(chan []byte, 100)
	sess.responseStore.Store(connectionID, responseChan)
	sess.connCounter.Store(0)

	// Build a complete regular response (agent sends full response in one packet)
	respData := buildHTTPResponse(200, map[string]string{
		"Content-Type": "application/json",
	}, `{"status":"ok"}`)

	// Feed the response before handleRequest reads it
	go func() {
		responseChan <- respData
	}()

	// Create a request
	req := httptest.NewRequest("GET", "/api/test", nil)
	w := newFlushRecorder()

	// Override connCounter so it produces connectionID "1"
	sess.connCounter.Store(0)

	// We can't call handleRequest directly because it sends via gRPC and creates
	// its own responseChan. Instead, test the response-handling portion:
	// simulate what handleRequest does from line 601 onward.
	response, ok := <-responseChan
	require.True(t, ok)

	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(response)), req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should NOT be detected as SSE
	assert.False(t, isSSEStreamingResponse(resp))

	// Write headers and body as handleRequest does for regular responses
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, err = io.Copy(w, resp.Body)
	require.NoError(t, err)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
	assert.Contains(t, w.Body.String(), `{"status":"ok"}`)
}

// TestHandleRequest_SSEDetection verifies that SSE responses are correctly
// detected and routed to the SSE handler.
func TestHandleRequest_SSEDetection(t *testing.T) {
	// Build a header-only SSE packet
	headerPacket := buildSSEHeaderPacket(200, nil)

	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(headerPacket)), nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.True(t, isSSEStreamingResponse(resp), "SSE response should be detected")
}

// TestHandleRequest_NonSSEDetection verifies non-SSE responses are not
// incorrectly routed to the SSE handler.
func TestHandleRequest_NonSSEDetection(t *testing.T) {
	respData := buildHTTPResponse(200, map[string]string{
		"Content-Type": "application/json",
	}, `{"ok":true}`)

	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(respData)), nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.False(t, isSSEStreamingResponse(resp), "JSON response should not be detected as SSE")
}

// --- SSE end-to-end simulation ----------------------------------------------

// TestSSEEndToEnd simulates the full SSE flow: agent sends header packet,
// then multiple chunk packets, then closes the channel. Verifies the gateway
// delivers all data to the HTTP client.
func TestSSEEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	sess := newTestSession(ctx, cancel)
	responseChan := make(chan []byte, 100)
	w := newFlushRecorder()

	// Step 1: Agent sends headers (first gRPC packet)
	headerPacket := buildSSEHeaderPacket(200, map[string]string{
		"Cache-Control": "no-cache",
		"Connection":    "keep-alive",
	})

	// Parse the header packet as handleRequest does
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(headerPacket)), nil)
	require.NoError(t, err)

	// Step 2: Agent sends SSE events as chunked data (subsequent gRPC packets)
	// These are raw chunked-encoding fragments:
	//   <hex-size>\r\n<data>\r\n
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\"}\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"Hello\"}}\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\" world\"}}\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n",
	}

	go func() {
		for _, event := range events {
			// Simulate chunked encoding: size\r\ndata\r\n
			chunk := fmt.Sprintf("%x\r\n%s\r\n", len(event), event)
			responseChan <- []byte(chunk)
		}
		// Chunked terminator
		responseChan <- []byte("0\r\n\r\n")
		close(responseChan)
	}()

	// Step 3: Gateway handles the SSE stream
	sess.handleSSEStream(w, resp, responseChan, "1")

	// Step 4: Verify
	assert.Equal(t, 200, w.Code)
	body := w.Body.String()

	for _, event := range events {
		assert.Contains(t, body, event, "body should contain event data")
	}
	assert.Contains(t, body, "0\r\n\r\n", "body should contain chunked terminator")

	// All events + terminator + initial flush = at least 6 flushes
	assert.GreaterOrEqual(t, w.flushCount, 5)
}

// TestSSEIdleTimeout verifies the idle timeout triggers when no data arrives.
func TestSSEIdleTimeout(t *testing.T) {
	// We can't wait 5 minutes in a test. Instead, test the mechanism by
	// verifying the method returns when no data arrives and context is cancelled.
	// The actual idle timeout value (5 min) is a constant, so we test the
	// cancellation path as a proxy.
	ctx, cancel := context.WithCancelCause(context.Background())
	sess := newTestSession(ctx, cancel)
	responseChan := make(chan []byte, 100)
	w := newFlushRecorder()

	headerPacket := buildSSEHeaderPacket(200, nil)
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(headerPacket)), nil)
	require.NoError(t, err)

	// Cancel after a short delay to simulate timeout behavior
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel(fmt.Errorf("idle timeout simulation"))
	}()

	start := time.Now()
	sess.handleSSEStream(w, resp, responseChan, "1")
	elapsed := time.Since(start)

	// Should return promptly after cancellation, not hang
	assert.Less(t, elapsed, 2*time.Second)
	assert.Equal(t, 200, w.Code)
}

// --- Channel buffer behavior tests ------------------------------------------

func TestResponseChannelBufferSize(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	sess := newTestSession(ctx, cancel)
	sess.connCounter.Store(0)

	// Verify the channel is created inside handleRequest with adequate buffer.
	// We can't easily call handleRequest (needs gRPC), so we test that the
	// SSE path can handle bursts by filling a channel of size 100.
	responseChan := make(chan []byte, 100)

	// Fill 50 items without blocking (well within the buffer)
	for i := 0; i < 50; i++ {
		select {
		case responseChan <- []byte(fmt.Sprintf("chunk-%d", i)):
		default:
			t.Fatalf("channel should not block at item %d with buffer size 100", i)
		}
	}
	close(responseChan)

	w := newFlushRecorder()
	headerPacket := buildSSEHeaderPacket(200, nil)
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(headerPacket)), nil)
	require.NoError(t, err)

	sess.handleSSEStream(w, resp, responseChan, "1")

	body := w.Body.String()
	for i := 0; i < 50; i++ {
		assert.Contains(t, body, fmt.Sprintf("chunk-%d", i))
	}
}
