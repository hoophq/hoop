package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/client/cmd/sidecartui"
)

// The screen's login is the terminal's, in two steps; Wait saves it, and a
// file booted from the screen is remembered for the next bare run.
func TestScreenLoginSavesAndRemembersTheFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	plane := &fakePlane{polls: []int{http.StatusOK}}
	srv := httptest.NewServer(plane.handler(t))
	defer srv.Close()

	sl := newScreenLogin(srv.URL).(*screenLogin)
	sl.l.minInterval = time.Millisecond
	ticket, err := sl.Begin(context.Background())
	if err != nil || ticket.Code != "BCDF-GHJK" || !strings.Contains(ticket.URL, "code=BCDF-GHJK") {
		t.Fatalf("ticket %+v, %v", ticket, err)
	}
	got, err := sl.Wait(context.Background())
	if err != nil || got.Name != "edge-1" || got.Token != plane.token {
		t.Fatalf("plane %+v, %v", got, err)
	}

	login := screenLoginFor(&sidecartui.Boot{ControlPlaneURL: srv.URL, Token: plane.token})
	if login == nil {
		t.Fatal("the saved login was not found for the boot")
	}
	if err := rememberSidecarConfig(login, "conf.yaml"); err != nil {
		t.Fatal(err)
	}
	again, err := loadSidecarLogin(sidecarStateDir(), srv.URL)
	if err != nil || again == nil || !filepath.IsAbs(again.Config) || filepath.Base(again.Config) != "conf.yaml" {
		t.Fatalf("remembered %+v, %v", again, err)
	}
	// A boot on a plane by token is not a login made on the screen.
	if screenLoginFor(&sidecartui.Boot{ControlPlaneURL: srv.URL, Token: "hsc_other"}) != nil {
		t.Error("a token the screen did not make was taken for its login")
	}
}

// fakePlane answers the two login routes the way the gateway does. polls
// lists the answers to successive polls; the last one repeats.
type fakePlane struct {
	mu        sync.Mutex
	token     string
	name      string
	polls     []int
	pollCount int
}

func (p *fakePlane) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sidecars/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Name string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		p.mu.Lock()
		p.token, p.name = r.Header.Get("hoop-sidecar-token"), body.Name
		p.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"user_code":"BCDF-GHJK","verification_url":"http://%s/sidecars/login?code=BCDF-GHJK","expires_in":600,"interval":0}`, r.Host)
	})
	mux.HandleFunc("POST /api/sidecars/login/poll", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if r.Header.Get("hoop-sidecar-token") != p.token {
			t.Errorf("poll sent another token than the start")
		}
		code := p.polls[min(p.pollCount, len(p.polls)-1)]
		p.pollCount++
		w.WriteHeader(code)
		if code == http.StatusOK {
			fmt.Fprint(w, `{"status":"approved","name":"edge-1"}`)
		}
	})
	return mux
}

func runLogin(t *testing.T, url string, open func(string) error) (*savedSidecarLogin, string, error) {
	t.Helper()
	var out bytes.Buffer
	l := &sidecarLoginer{planeURL: url, name: "laptop", out: &out, client: sidecarLoginHTTPClient(),
		open: open, minInterval: time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := l.run(ctx)
	return got, out.String(), err
}

func TestSidecarLoginWaitsForApprovalAndKeepsItsOwnToken(t *testing.T) {
	plane := &fakePlane{polls: []int{http.StatusPreconditionRequired, http.StatusPreconditionRequired, http.StatusOK}}
	srv := httptest.NewServer(plane.handler(t))
	defer srv.Close()

	var opened string
	got, out, err := runLogin(t, srv.URL+"/", func(u string) error { opened = u; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Token, "hsc_") || len(got.Token) < 40 {
		t.Fatalf("token %q is not a sidecar token", got.Token)
	}
	if got.Token != plane.token {
		t.Fatal("the saved token is not the one the plane received")
	}
	if got.URL != srv.URL || got.Name != "edge-1" {
		t.Fatalf("got %+v", got)
	}
	if plane.name != "laptop" {
		t.Fatalf("asked for name %q", plane.name)
	}
	if !strings.Contains(out, "BCDF-GHJK") || !strings.Contains(opened, "code=BCDF-GHJK") {
		t.Fatalf("the code was not shown or opened; out=%q opened=%q", out, opened)
	}
	if plane.pollCount != 3 {
		t.Fatalf("polled %d times, want 3", plane.pollCount)
	}
}

func TestSidecarLoginEndsOnDenialAndExpiry(t *testing.T) {
	for code, want := range map[int]string{
		http.StatusForbidden: "denied",
		http.StatusGone:      "expired",
	} {
		plane := &fakePlane{polls: []int{code}}
		srv := httptest.NewServer(plane.handler(t))
		_, _, err := runLogin(t, srv.URL, nil)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("poll %d: err = %v, want %q", code, err, want)
		}
	}
}

// A plane from before this feature answers 404; say what to do instead of
// printing a status code.
func TestSidecarLoginAgainstAnOlderPlane(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	_, _, err := runLogin(t, srv.URL, nil)
	if err == nil || !strings.Contains(err.Error(), "does not support sidecar login") {
		t.Fatalf("err = %v", err)
	}
}

// The token is the credential: plain http to another host would send it in
// the clear, and the login refuses before making one.
func TestSidecarLoginRefusesPlainHTTPToAnotherHost(t *testing.T) {
	_, _, err := runLogin(t, "http://cp.example.com", nil)
	if err == nil || !strings.Contains(err.Error(), "unencrypted") {
		t.Fatalf("err = %v", err)
	}
}

func TestSavedSidecarLoginIsPrivateAndPerPlane(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sidecar")
	path, err := saveSidecarLogin(dir, savedSidecarLogin{URL: "https://use.hoop.dev", Token: "hsc_x", Name: "edge"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", info.Mode().Perm())
	}
	got, err := loadSidecarLogin(dir, "https://use.hoop.dev/")
	if err != nil || got == nil || got.Token != "hsc_x" {
		t.Fatalf("got %+v, %v", got, err)
	}
	// Another plane never receives this token.
	if got, err := loadSidecarLogin(dir, "https://cp.example.com"); err != nil || got != nil {
		t.Fatalf("another plane got %+v, %v", got, err)
	}
	if got, err := loadSidecarLogin(filepath.Join(t.TempDir(), "none"), "https://use.hoop.dev"); err != nil || got != nil {
		t.Fatalf("no file: %+v, %v", got, err)
	}
}

// The free plan always runs identified: every run that serves logs in, unless
// it has a credential or a license. --validate and --migrate never log in.
func TestWantsSidecarLogin(t *testing.T) {
	for _, tt := range []struct {
		name    string
		run     sidecarLoginRun
		want    bool
		wantErr string
	}{
		{"a run with nothing", sidecarLoginRun{}, true, ""},
		{"a config file still logs in", sidecarLoginRun{}, true, ""},
		{"a token", sidecarLoginRun{credential: true}, false, ""},
		{"a license runs on its own", sidecarLoginRun{licensed: true}, false, ""},
		{"--no-login with a license", sidecarLoginRun{noLogin: true, licensed: true}, false, ""},
		{"--no-login on the free plan", sidecarLoginRun{noLogin: true}, false, "free plan runs logged in"},
		{"--validate or --migrate", sidecarLoginRun{oneShot: true}, false, ""},
		{"--login", sidecarLoginRun{login: true}, true, ""},
		{"--login with a license", sidecarLoginRun{login: true, licensed: true}, true, ""},
		{"--login with --no-login", sidecarLoginRun{login: true, noLogin: true}, false, "contradict"},
		{"--login with a token", sidecarLoginRun{login: true, credential: true}, false, "drop --token"},
		{"--login with --validate", sidecarLoginRun{login: true, oneShot: true}, false, "does not combine"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := wantsSidecarLogin(tt.run)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestSidecarNameFromHost(t *testing.T) {
	for host, want := range map[string]string{
		"Matms-MacBook-Pro.local":  "matms-macbook-pro",
		"ip-10-0-0-1.ec2.internal": "ip-10-0-0-1",
		"a":                        "sidecar",
		"":                         "sidecar",
		"web_01":                   "web-01",
	} {
		if got := sidecarNameFromHost(host); got != want {
			t.Errorf("%q: got %q, want %q", host, got, want)
		}
	}
}
