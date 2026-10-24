package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// One login per control plane: a token is read back only for the plane that
// issued it, and saving one plane's login keeps the others'.
func TestSidecarCredentialsArePerPlane(t *testing.T) {
	dir := t.TempDir()
	if _, err := saveSidecarLogin(dir, savedSidecarLogin{URL: "https://use.hoop.dev/", Token: "hsc_a", Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := saveSidecarLogin(dir, savedSidecarLogin{URL: "https://cp.example.com", Token: "hsc_b", Name: "b"}); err != nil {
		t.Fatal(err)
	}
	for plane, want := range map[string]string{"https://use.hoop.dev": "hsc_a", "https://cp.example.com/": "hsc_b"} {
		got, err := loadSidecarLogin(dir, plane)
		if err != nil || got == nil || got.Token != want {
			t.Errorf("%s: got %+v, %v; want %s", plane, got, err, want)
		}
	}
	if got, _ := loadSidecarLogin(dir, "https://other.example.com"); got != nil {
		t.Errorf("another plane got %+v", got)
	}
}

func TestSidecarStatusShowsTheTokenOnlyWhenAsked(t *testing.T) {
	dir := t.TempDir()
	_, _ = saveSidecarLogin(dir, savedSidecarLogin{URL: DefaultSidecarControlPlaneURL, Token: "hsc_secret", Name: "edge",
		Config: "/etc/sidecar.yaml"})
	env := func(string) string { return "" }

	var out bytes.Buffer
	if err := printSidecarStatus(&out, dir, env, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{DefaultSidecarControlPlaneURL, "hoop, free plan", "edge", "/etc/sidecar.yaml"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status does not show %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "hsc_secret") {
		t.Error("status printed the token without --token")
	}
	out.Reset()
	_ = printSidecarStatus(&out, dir, env, true)
	if !strings.Contains(out.String(), "hsc_secret") {
		t.Error("--token did not print the token")
	}
}

func TestSidecarLogout(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	// One login: logout needs no flag.
	_, _ = saveSidecarLogin(dir, savedSidecarLogin{URL: "https://use.hoop.dev", Token: "hsc_a"})
	if err := sidecarLogout(&out, dir, "", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := loadSidecarLogin(dir, "https://use.hoop.dev"); got != nil {
		t.Fatal("the login survived logout")
	}

	// Two: logout asks which, then forgets only that one.
	_, _ = saveSidecarLogin(dir, savedSidecarLogin{URL: "https://use.hoop.dev", Token: "hsc_a"})
	_, _ = saveSidecarLogin(dir, savedSidecarLogin{URL: "https://cp.example.com", Token: "hsc_b"})
	if err := sidecarLogout(&out, dir, "", false); err == nil || !strings.Contains(err.Error(), "--control-plane") {
		t.Fatalf("two logins and no flag: err = %v", err)
	}
	if err := sidecarLogout(&out, dir, "https://cp.example.com/", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := loadSidecarLogin(dir, "https://use.hoop.dev"); got == nil {
		t.Fatal("logout of one plane forgot the other")
	}
	if err := sidecarLogout(&out, dir, "", true); err != nil {
		t.Fatal(err)
	}
	if logins, _ := listSidecarLogins(dir); len(logins) != 0 {
		t.Fatalf("--all left %v", logins)
	}
}

// A token with no plane named anywhere is a token from the hosted plane: a
// headless free sidecar sets HOOP_SIDECAR_TOKEN and nothing else.
func TestATokenAloneGoesToTheHostedPlane(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HOOP_CONTROL_PLANE_URL", "")
	t.Setenv("HOOP_SIDECAR_TOKEN", "hsc_headless")
	defer func(c string) { sidecarConfigFlag = c }(sidecarConfigFlag)
	sidecarConfigFlag = ""

	login, _, choose, err := resolveSidecarLogin(startSidecarCmd, nil, false)
	if err != nil || login != nil || choose {
		t.Fatalf("a token logged in again: login=%+v choose=%v err=%v", login, choose, err)
	}
	if got := os.Getenv("HOOP_CONTROL_PLANE_URL"); got != DefaultSidecarControlPlaneURL {
		t.Fatalf("the plane is %q, want the hosted one", got)
	}

	// A plane named in the environment is kept: an Enterprise deployment.
	t.Setenv("HOOP_CONTROL_PLANE_URL", "https://cp.example.com")
	if _, _, _, err := resolveSidecarLogin(startSidecarCmd, nil, false); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("HOOP_CONTROL_PLANE_URL"); got != "https://cp.example.com" {
		t.Fatalf("the Enterprise plane became %q", got)
	}
}

// A platform identity with no plane named is not sent to the hosted plane:
// its token belongs to the customer's own cluster.
func TestAnIdentityAloneIsNotSentToTheHostedPlane(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HOOP_CONTROL_PLANE_URL", "")
	t.Setenv("HOOP_SIDECAR_TOKEN", "")
	t.Setenv("HOOP_SIDECAR_IDENTITY_TYPE", "kubernetes")
	defer func(c string) { sidecarConfigFlag = c }(sidecarConfigFlag)
	sidecarConfigFlag = ""

	if _, _, _, err := resolveSidecarLogin(startSidecarCmd, nil, false); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("HOOP_CONTROL_PLANE_URL"); got != "" {
		t.Fatalf("an identity went to %q, want no plane", got)
	}
}

// A headless login writes JSON lines a marketplace console or an agent can
// read: the link and code first, the approval last.
func TestSidecarLoginWritesMachineReadableEvents(t *testing.T) {
	plane := &fakePlane{polls: []int{http.StatusPreconditionRequired, http.StatusOK}}
	srv := httptest.NewServer(plane.handler(t))
	defer srv.Close()
	var events, human bytes.Buffer
	l := &sidecarLoginer{planeURL: srv.URL, name: "edge", out: &human, client: sidecarLoginHTTPClient(),
		minInterval: time.Millisecond, events: &events}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := l.run(ctx); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(events.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 JSON lines, got %d:\n%s", len(lines), events.String())
	}
	var first, last sidecarLoginEvent
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &last); err != nil {
		t.Fatal(err)
	}
	if first.Event != "sidecar_login_required" || first.UserCode != "BCDF-GHJK" ||
		!strings.Contains(first.VerificationURL, "code=BCDF-GHJK") || first.Plane != srv.URL {
		t.Errorf("first event %+v", first)
	}
	if last.Event != "sidecar_login_approved" || last.Sidecar != "edge-1" {
		t.Errorf("last event %+v", last)
	}
	if !strings.Contains(human.String(), "BCDF-GHJK") {
		t.Error("the person reading the log lost the human text")
	}
}
