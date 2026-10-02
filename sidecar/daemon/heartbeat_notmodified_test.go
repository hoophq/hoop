package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// After one full answer, the heartbeat names that document and the plane
// answers 304. The sidecar keeps serving it and reports it as running, the
// same as a full answer holding the same bytes.
func TestANotModifiedHandshakeKeepsTheServedDocument(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	drifted := editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["truncate"]`)

	var mu sync.Mutex
	var seen []handshakeRequest
	notModified := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var hs handshakeRequest
		_ = json.NewDecoder(r.Body).Decode(&hs)
		mu.Lock()
		seen = append(seen, hs)
		mu.Unlock()
		w.Header().Set(ConfigRevisionHeader, "rev-2")
		w.Header().Set(LicenseManagedHeader, "true")
		if hs.ServedRevision == "rev-2" {
			mu.Lock()
			notModified++
			mu.Unlock()
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte(drifted))
	}))
	defer srv.Close()
	cp := &controlPlane{url: srv.URL, token: "hsc_x", every: time.Millisecond,
		revision: "rev-1", outcome: reloadApplied.String()}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	cp.heartbeat(ctx, slog.New(slog.NewTextHandler(buf, nil)), rl)

	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 3 || notModified == 0 {
		t.Fatalf("want a full answer then 304s, got %d handshakes and %d 304s", len(seen), notModified)
	}
	if seen[0].ServedRevision != "" {
		t.Errorf("the first heartbeat named %q; nothing was served in full yet", seen[0].ServedRevision)
	}
	for i, hs := range seen[2:] {
		if hs.ServedRevision != "rev-2" || hs.AppliedRevision != "rev-2" || hs.LastOutcome != "unchanged" {
			t.Errorf("handshake %d = %+v, want rev-2 served and unchanged", i+2, hs)
		}
	}
	if strings.Contains(buf.String(), "handshake failed") {
		t.Errorf("a 304 was logged as a failure:\n%s", buf)
	}
}

// A 304 to a handshake that named no document leaves the sidecar nothing to
// serve. It is an error, never an empty document.
func TestANotModifiedAnswerWithoutAServedRevisionIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	_, err := fetchControlPlaneConfig(srv.URL, "hsc_x", handshakeRequest{Version: "1.2.3"})
	if err == nil || errors.Is(err, errNotModified) {
		t.Fatalf("err = %v, want an error that is not errNotModified", err)
	}
}

// Each heartbeat wait stays inside every ± heartbeatJitter, and the waits
// differ, so a fleet started together does not stay in step.
func TestTheHeartbeatWaitIsJittered(t *testing.T) {
	low := time.Duration(float64(heartbeatEvery) * (1 - heartbeatJitter))
	high := time.Duration(float64(heartbeatEvery) * (1 + heartbeatJitter))
	distinct := map[time.Duration]bool{}
	for range 200 {
		d := jittered(heartbeatEvery)
		if d < low || d >= high {
			t.Fatalf("wait %s is outside [%s, %s)", d, low, high)
		}
		distinct[d] = true
	}
	if len(distinct) < 2 {
		t.Error("every wait was the same")
	}
}
