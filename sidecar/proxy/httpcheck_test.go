package proxy_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	httpcodec "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
)

type recordingModel struct {
	mu     sync.Mutex
	inputs []string
}

func (*recordingModel) Name() string { return "stub" }

func (m *recordingModel) Classify(_ context.Context, _, content string) (*analyzer.Result, error) {
	m.mu.Lock()
	m.inputs = append(m.inputs, content)
	m.mu.Unlock()
	return &analyzer.Result{RiskLevel: analyzer.RiskLow}, nil
}

func (m *recordingModel) all() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.inputs...)
}

type upstreamRequest struct {
	method, uri, auth, marker string
	conn                      string
}

// ordersUpstream answers a GET with the order, echoing the caller's
// Authorization, and any other method with 204.
func ordersUpstream(t *testing.T) (string, func() []upstreamRequest) {
	t.Helper()
	var mu sync.Mutex
	var got []upstreamRequest
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, upstreamRequest{r.Method, r.RequestURI, r.Header.Get("Authorization"),
			r.Header.Get("Hoop-Resource-Check"), r.RemoteAddr})
		mu.Unlock()
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1,"status":"open","seen_auth":"`+r.Header.Get("Authorization")+`"}`)
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String(), func() []upstreamRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]upstreamRequest(nil), got...)
	}
}

func checkingHTTPLane(t *testing.T, upstream string, model analyzer.Provider) *proxy.Server {
	t.Helper()
	ev, err := analyzer.New(analyzer.Config{Rule: "api", Provider: model, Trigger: analyzer.Trigger{All: true},
		ResourceCheck: true})
	if err != nil {
		t.Fatal(err)
	}
	return startServer(t, proxy.Config{
		Upstream: upstream, Protocol: inspect.HTTP, Connection: "api", Policy: ev,
		CodecFactory: func() inspect.Codec { return httpcodec.New(httpcodec.Options{}) },
	})
}

func TestAnHTTPCheckReadsTheTargetOnItsOwnConnection(t *testing.T) {
	addr, requests := ordersUpstream(t)
	model := &recordingModel{}
	srv := checkingHTTPLane(t, addr, model)

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	const auth = "Bearer tok-0123456789"
	if _, err := io.WriteString(c, "DELETE /orders/1 HTTP/1.1\r\nHost: shop\r\nAuthorization: "+auth+"\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("the client got %d, want the DELETE's 204", resp.StatusCode)
	}
	_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if extra, _ := br.Peek(1); len(extra) > 0 {
		t.Fatalf("the client got bytes after the DELETE's response: %q", extra)
	}

	got := requests()
	if len(got) != 2 || got[0].method != "GET" || got[1].method != "DELETE" {
		t.Fatalf("upstream got %+v, want the GET then the DELETE", got)
	}
	if got[0].uri != "/orders/1" || got[0].auth != auth || got[0].marker != "1" {
		t.Errorf("the GET = %+v", got[0])
	}
	if got[0].conn == got[1].conn {
		t.Error("the GET went on the client's upstream connection")
	}
	inputs := model.all()
	if len(inputs) != 1 || !strings.Contains(inputs[0], `"status":"open"`) {
		t.Fatalf("model inputs = %q", inputs)
	}
	if strings.Contains(inputs[0], "tok-0123456789") {
		t.Errorf("the bearer token reached the model: %s", inputs[0])
	}
}

func TestAnHTTPCheckSkipsOtherMethods(t *testing.T) {
	addr, requests := ordersUpstream(t)
	model := &recordingModel{}
	srv := checkingHTTPLane(t, addr, model)

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(c)
	for _, req := range []string{"GET /orders/1", "POST /orders"} {
		if _, err := io.WriteString(c, req+" HTTP/1.1\r\nHost: shop\r\nContent-Length: 0\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	if got := requests(); len(got) != 2 {
		t.Errorf("upstream got %+v, want only the client's two requests", got)
	}
	for _, in := range model.all() {
		if strings.Contains(in, "Resource check") {
			t.Errorf("a GET or POST was checked: %s", in)
		}
	}
}
