package sidecartui

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Signed out, the TUI offers only the two ways in, the account first, and no
// key reaches the setup screens or the file picker.
func TestSignedOutTheHomeOffersOnlyTheWaysIn(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), "x: 1\n")
	m := signedOut(homeModel(t, dir, validateFile))
	var ids []string
	for _, it := range m.home.items {
		ids = append(ids, it.id)
	}
	if len(ids) != 2 || ids[0] != "account" || ids[1] != "connect" {
		t.Fatalf("signed-out home = %v, want [account connect]", ids)
	}
	for _, k := range []string{"w", "s", "o"} {
		tm, _ := m.Update(wkey(k))
		fm := tm.(firstRunModel)
		if fm.wiz != nil || fm.detecting || fm.picker != nil {
			t.Errorf("%q reached a config screen before sign-in", k)
		}
	}
}

// The pairing hands the token to this process only with its own state, then
// sends the browser on to the sidecar's page.
func TestThePairingTakesTheTokenWithItsState(t *testing.T) {
	p, err := startPairing("https://use.example.com/", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	page, err := url.Parse(p.PageURL)
	if err != nil || page.Path != "/sidecars/pair" || page.Query().Get("name") != "laptop" {
		t.Fatalf("page URL = %s", p.PageURL)
	}
	callback := page.Query().Get("callback")
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	bad, err := noFollow.Get(callback + "?state=wrong&token=hsc_x&id=sc-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = bad.Body.Close()
	if bad.StatusCode != http.StatusForbidden {
		t.Fatalf("a wrong state answered %d, want 403", bad.StatusCode)
	}

	q := url.Values{"state": {page.Query().Get("state")}, "token": {"hsc_x"}, "id": {"sc-1"}, "name": {"laptop"}}
	ok, err := noFollow.Get(callback + "?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	_ = ok.Body.Close()
	if ok.StatusCode != http.StatusFound || ok.Header.Get("Location") != "https://use.example.com/sidecars/sc-1" {
		t.Fatalf("pairing answered %d to %q", ok.StatusCode, ok.Header.Get("Location"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cred, err := p.wait(ctx)
	if err != nil || cred.URL != "https://use.example.com" || cred.Token != "hsc_x" || cred.ID != "sc-1" {
		t.Fatalf("wait = %+v, %v", cred, err)
	}
}

// The credential is stored for this user only and read back whole; a file
// missing one half is refused, never read as signed in.
func TestTheCredentialIsStoredPrivately(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sidecar")
	want := PlaneCredential{URL: "https://use.example.com", Token: "hsc_x", Name: "laptop", ID: "sc-1"}
	if err := SavePlaneCredential(dir, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, PlaneCredentialFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("stored file mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	got, err := LoadPlaneCredential(dir)
	if err != nil || got == nil || *got != want {
		t.Fatalf("loaded %+v, %v; want %+v", got, err, want)
	}
	writeFile(t, filepath.Join(dir, PlaneCredentialFile), `{"control_plane_url":"https://use.example.com"}`)
	if got, err := LoadPlaneCredential(dir); err == nil || got != nil {
		t.Fatalf("a credential with no token loaded as %+v, %v", got, err)
	}
	if got, err := LoadPlaneCredential(t.TempDir()); err != nil || got != nil {
		t.Fatalf("no file = %+v, %v; want nil, nil", got, err)
	}
}

// A finished pairing signs in, stores the credential and goes on to set up a
// config; the config booted later goes to that Control Plane.
func TestAPairingSignsInAndTheBootUsesIt(t *testing.T) {
	credDir := t.TempDir()
	m := signedOut(homeModel(t, t.TempDir(), validateFile))
	m.credDir = credDir
	cred := PlaneCredential{URL: "https://use.example.com", Token: "hsc_x", ID: "sc-1"}

	tm, _ := m.Update(frPairedMsg{cred: cred})
	fm := tm.(firstRunModel)
	if !fm.signedIn() || !(fm.detecting || fm.wiz != nil) {
		t.Fatalf("after pairing: signedIn=%v detecting=%v wiz=%v", fm.signedIn(), fm.detecting, fm.wiz != nil)
	}
	if stored, err := LoadPlaneCredential(credDir); err != nil || stored == nil || stored.Token != "hsc_x" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}

	// Later, with only the stored credential: a checked config boots on it.
	fm.wiz, fm.detecting, fm.plane = nil, false, nil
	fm.checking = "/tmp/a.yaml"
	tm, _ = fm.Update(frCheckedMsg{path: "/tmp/a.yaml"})
	boot := tm.(firstRunModel).boot
	if boot == nil || boot.ControlPlaneURL != cred.URL || boot.Token != cred.Token {
		t.Fatalf("boot = %+v, want it on %s", boot, cred.URL)
	}
}

// esc while the browser pairs stops waiting and comes back to the ways in.
func TestEscStopsAPairing(t *testing.T) {
	m := signedOut(homeModel(t, t.TempDir(), validateFile))
	m.saasURL = "https://use.example.com"
	m.openURL = func(string) error { return nil }
	var tm tea.Model = m
	for tm.(firstRunModel).home.selected() != "account" {
		tm, _ = tm.Update(wkey("down"))
	}
	tm, cmd := tm.Update(wkey("enter"))
	if tm.(firstRunModel).pair == nil || cmd == nil {
		t.Fatal("enter on the account did not start a pairing")
	}
	tm, _ = tm.Update(wkey("esc"))
	msg := cmd()
	tm, _ = tm.Update(msg)
	fm := tm.(firstRunModel)
	if fm.pair != nil || fm.signedIn() || fm.pairBad {
		t.Fatalf("after esc: pair=%v signedIn=%v status=%q", fm.pair != nil, fm.signedIn(), fm.pairStatus)
	}
}
