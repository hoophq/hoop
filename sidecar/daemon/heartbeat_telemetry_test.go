package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	telem "github.com/hoophq/hoop/sidecar/telemetry"
)

// handshakeRecorder satisfies telem.Exporter with the one method the
// heartbeat calls.
type handshakeRecorder struct {
	telem.Exporter
	mu  sync.Mutex
	got []telem.Handshake
}

func (r *handshakeRecorder) ControlPlaneHandshake(h telem.Handshake) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, h)
}

func (r *handshakeRecorder) handshakes() []telem.Handshake {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]telem.Handshake(nil), r.got...)
}

// Each heartbeat is counted once, with the outcome of its handshake: an
// answered one, a plane that answers an error, and a plane that is gone.
func TestTheHeartbeatCountsEachHandshakeByOutcome(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		closed  bool
		want    string
	}{
		{name: "answered", want: telem.HandshakeOK, handler: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(reloadBase))
		}},
		{name: "server error", want: telem.HandshakeError, handler: func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"message":"down"}`, http.StatusServiceUnavailable)
		}},
		{name: "plane gone", want: telem.HandshakeUnreachable, closed: true, handler: func(http.ResponseWriter, *http.Request) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rl, _ := testReloader(t, reloadBase)
			srv := httptest.NewServer(tc.handler)
			if tc.closed {
				srv.Close()
			} else {
				defer srv.Close()
			}
			rec := &handshakeRecorder{}
			cp := &controlPlane{url: srv.URL, cred: tokenCredential("hsc_x"), every: time.Millisecond,
				otel: &otelExporter{exp: rec, log: slog.New(slog.NewTextHandler(io.Discard, nil))}}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
			defer cancel()
			cp.heartbeat(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), rl)

			got := rec.handshakes()
			if len(got) == 0 {
				t.Fatal("no handshake was counted")
			}
			for i, h := range got {
				if h.Kind != telem.HandshakeHeartbeat || h.Outcome != tc.want {
					t.Errorf("handshake %d = %s/%s, want heartbeat/%s", i, h.Kind, h.Outcome, tc.want)
				}
				if (h.Err == nil) != (tc.want == telem.HandshakeOK) {
					t.Errorf("handshake %d: err = %v with outcome %s", i, h.Err, h.Outcome)
				}
			}
		})
	}
}

// The outcome reads the errors fetchControlPlaneConfig returns, as it
// returns them: wrapped.
func TestHandshakeOutcome(t *testing.T) {
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer tlsSrv.Close()
	_, certErr := fetchControlPlaneConfig(tlsSrv.URL, tokenCredential("hsc_x"), handshakeRequest{Version: "test"})
	if certErr == nil {
		t.Fatal("a plane with an untrusted certificate answered")
	}

	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, telem.HandshakeOK},
		{"412", fmt.Errorf("%w (at http://plane)", ErrPlaneHasNoConfig), telem.HandshakeNoConfig},
		{"412 with no file", planeHasNoConfigError{planeURL: "http://plane"}, telem.HandshakeNoConfig},
		{"untrusted certificate", certErr, telem.HandshakeError},
		{"plain", errors.New("the control plane at http://plane answered 500"), telem.HandshakeError},
	} {
		if got := handshakeOutcome(tc.err); got != tc.want {
			t.Errorf("%s: handshakeOutcome(%v) = %s, want %s", tc.name, tc.err, got, tc.want)
		}
	}
}
