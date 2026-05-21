package httpclient

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestVersionCheckCallbackInvokedOnResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "hoopgateway/9.9.9")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	prev := VersionCheckCallback
	t.Cleanup(func() { VersionCheckCallback = prev })

	var (
		mu      sync.Mutex
		got     string
		callCnt atomic.Int32
	)
	VersionCheckCallback = func(h string) {
		mu.Lock()
		defer mu.Unlock()
		got = h
		callCnt.Add(1)
	}

	c := NewHttpClient("")
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()

	if callCnt.Load() != 1 {
		t.Fatalf("callback called %d times, want 1", callCnt.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if got != "hoopgateway/9.9.9" {
		t.Fatalf("callback got %q, want %q", got, "hoopgateway/9.9.9")
	}
}

// TestNoCallbackMeansNoWrap guarantees that callers that never set the
// callback keep using the unwrapped transport. This protects the agent
// and the gateway from accidental coupling to the CLI's behavior.
func TestNoCallbackMeansNoWrap(t *testing.T) {
	prev := VersionCheckCallback
	VersionCheckCallback = nil
	t.Cleanup(func() { VersionCheckCallback = prev })

	c := NewHttpClient("").(*httpClient)
	if _, ok := c.client.Transport.(*versionCheckRT); ok {
		t.Fatalf("transport should not be wrapped when VersionCheckCallback is nil")
	}
}
