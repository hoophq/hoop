package daemon

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The redirect is the whole data path of first-run mode: every path answers
// 302 toward the live guide. 302 and not 301, because a browser caches a
// 301 per host:port and the next process on the port would inherit it.
func TestFirstRunRedirectsEveryPathToTheGuide(t *testing.T) {
	h := firstRunRedirect()
	for _, path := range []string{"/", "/anything", "/favicon.ico"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))

		if rec.Code != http.StatusFound {
			t.Errorf("%s: status = %d, want %d", path, rec.Code, http.StatusFound)
		}
		if loc := rec.Header().Get("Location"); loc != FirstRunDocsURL {
			t.Errorf("%s: Location = %q, want %q", path, loc, FirstRunDocsURL)
		}
	}
}

// A busy preferred port must move the lane, not kill it: that is the
// difference between "the demo works" and "the demo fails exactly when
// something else is already running", which is most laptops.
func TestFirstRunListenFallsBackWhenThePortIsBusy(t *testing.T) {
	squatter, err := net.Listen("tcp", firstRunAddr)
	if err != nil {
		// Something on this machine already holds the port, which IS the
		// busy case: firstRunListen must still land somewhere.
		ln, fellBack, err := firstRunListen()
		if err != nil {
			t.Fatalf("with the port busy, firstRunListen failed: %v", err)
		}
		defer ln.Close()
		if !fellBack {
			t.Error("the preferred port is held by another process, yet fellBack is false")
		}
		return
	}
	defer squatter.Close()

	ln, fellBack, err := firstRunListen()
	if err != nil {
		t.Fatalf("firstRunListen with %s busy: %v", firstRunAddr, err)
	}
	defer ln.Close()

	if !fellBack {
		t.Error("the preferred port was busy, yet fellBack is false")
	}
	if ln.Addr().String() == firstRunAddr {
		t.Errorf("bound %s while it was already held", firstRunAddr)
	}
}

// The banner is the interface: it must carry the URL that was actually
// bound, say the config is a default, and name the command that replaces
// it. When the port moved, it must say so.
func TestFirstRunBannerNamesTheBoundURLAndTheNextStep(t *testing.T) {
	var buf bytes.Buffer
	firstRunBanner(&buf, "127.0.0.1:49152", false, "hoop start sidecar --config config.yaml")

	out := buf.String()
	for _, want := range []string{
		"http://127.0.0.1:49152",
		FirstRunDocsURL,
		"default",
		"hoop start sidecar --config config.yaml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("banner is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "busy") {
		t.Errorf("no fallback happened, yet the banner mentions a busy port:\n%s", out)
	}

	buf.Reset()
	firstRunBanner(&buf, "127.0.0.1:49153", true, "hoop-inspect -config config.yaml")
	if out := buf.String(); !strings.Contains(out, "busy") {
		t.Errorf("the port moved and the banner does not say so:\n%s", out)
	}
}

// End to end minus the process: firstRunServe binds, answers a real HTTP
// request with the redirect, and a cancelled context shuts it down cleanly
// — the exit an operator's Ctrl-C takes, and it must be exit 0.
func TestFirstRunServeAnswersAndStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- firstRunServe(ctx, &buf, "hoop start sidecar --config config.yaml")
	}()

	url := waitForBannerURL(t, &buf)

	client := &http.Client{
		// The response under test is the redirect itself.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 5 * time.Second,
	}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	if loc := resp.Header.Get("Location"); loc != FirstRunDocsURL {
		t.Errorf("Location = %q, want %q", loc, FirstRunDocsURL)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a cancelled first run returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("firstRunServe did not stop after the context was cancelled")
	}
}

// waitForBannerURL polls the banner buffer until the "Open http://..." line
// lands and returns that URL. The banner is written before the server
// starts accepting, so once the URL is visible the GET below may still race
// the Serve goroutine by a scheduler tick; the poll in the caller's Get is
// the client timeout.
func waitForBannerURL(t *testing.T, buf *bytes.Buffer) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out := buf.String()
		if i := strings.Index(out, "http://"); i >= 0 {
			end := strings.IndexAny(out[i:], " \n")
			if end > 0 {
				return out[i : i+end]
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the banner never printed a URL")
	return ""
}

// The default lane must never leave loopback: it is unauthenticated and
// exists only for the human at this terminal.
func TestFirstRunListenBindsLoopback(t *testing.T) {
	ln, _, err := firstRunListen()
	if err != nil {
		t.Fatalf("firstRunListen: %v", err)
	}
	defer ln.Close()

	host, _, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("bound address %q does not split: %v", ln.Addr(), err)
	}
	if host != "127.0.0.1" {
		t.Errorf("bound host = %q, want 127.0.0.1", host)
	}
}
