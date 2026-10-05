package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// The plane serves a document this build refuses. Every heartbeat after the
// first says so, with the reason, and the applied revision never moves to the
// refused document: the plane must be able to tell this sidecar from one that
// took the document, for as long as the refusal lasts.
func TestTheHeartbeatReportsARefusalUntilItClears(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	unknown := editJSON(t, reloadBase, `"log_level": "info"`, `"log_level": "info", "future_key": true`)

	var mu sync.Mutex
	var seen []handshakeRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var hs handshakeRequest
		_ = json.NewDecoder(r.Body).Decode(&hs)
		mu.Lock()
		seen = append(seen, hs)
		mu.Unlock()
		w.Header().Set(ConfigRevisionHeader, "rev-2")
		w.Header().Set(LicenseManagedHeader, "true")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(unknown))
	}))
	defer srv.Close()
	cp := &controlPlane{url: srv.URL, cred: tokenCredential("hsc_x"), every: time.Millisecond,
		revision: "rev-1", outcome: reloadApplied.String()}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	cp.heartbeat(ctx, slog.New(slog.NewTextHandler(buf, nil)), rl)

	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 3 {
		t.Fatalf("want at least three handshakes, got %d", len(seen))
	}
	if seen[0].LastOutcome != "applied" || seen[0].AppliedRevision != "rev-1" {
		t.Errorf("the first handshake must report the boot document: %+v", seen[0])
	}
	for i, hs := range seen[1:] {
		if hs.LastOutcome != "refused" || hs.AppliedRevision != "rev-1" {
			t.Errorf("handshake %d = %+v, want refused on rev-1", i+1, hs)
		}
		if !strings.Contains(hs.LastError, `unknown field "future_key"`) {
			t.Errorf("handshake %d does not carry the reason: %q", i+1, hs.LastError)
		}
	}
	if cp.revision != "rev-1" {
		t.Errorf("the running revision moved to %q while the document was refused", cp.revision)
	}
}
