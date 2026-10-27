package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	mustSaveSidecarLogin(t, dir, savedSidecarLogin{URL: DefaultSidecarControlPlaneURL, Token: "hsc_secret", Name: "edge",
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
	mustSaveSidecarLogin(t, dir, savedSidecarLogin{URL: "https://use.hoop.dev", Token: "hsc_a"})
	if err := sidecarLogout(&out, dir, "", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := loadSidecarLogin(dir, "https://use.hoop.dev"); got != nil {
		t.Fatal("the login survived logout")
	}

	// Two: logout asks which, then forgets only that one.
	mustSaveSidecarLogin(t, dir, savedSidecarLogin{URL: "https://use.hoop.dev", Token: "hsc_a"})
	mustSaveSidecarLogin(t, dir, savedSidecarLogin{URL: "https://cp.example.com", Token: "hsc_b"})
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

// A headless free install (ENG-906) sets HOOP_SIDECAR_FREE_STANDALONE: its
// file runs with no login and no token, as before 1.268.0.
func TestFreeStandaloneRunsTheFileWithoutALogin(t *testing.T) {
	plane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("free standalone called the plane: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer plane.Close()
	// A login, if one ran, reads the command's context.
	defer startSidecarCmd.SetContext(startSidecarCmd.Context())
	startSidecarCmd.SetContext(context.Background())
	t.Setenv("HOME", t.TempDir())
	t.Setenv(SidecarStateDirEnv, t.TempDir())
	t.Setenv("HOOP_CONTROL_PLANE_URL", plane.URL)
	t.Setenv("HOOP_SIDECAR_TOKEN", "")
	t.Setenv("HOOP_SIDECAR_IDENTITY_TYPE", "")
	t.Setenv("HOOP_LICENSE", "")
	t.Setenv(sidecarFreeStandaloneEnv, "true")
	defer func(c string) { sidecarConfigFlag = c }(sidecarConfigFlag)
	sidecarConfigFlag = filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(sidecarConfigFlag, []byte("listeners: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	login, _, choose, err := resolveSidecarLogin(startSidecarCmd, nil, false)
	if err != nil || login != nil || choose {
		t.Fatalf("free standalone logged in: login=%+v choose=%v err=%v", login, choose, err)
	}

	t.Setenv(sidecarFreeStandaloneEnv, "yes please")
	if _, _, _, err := resolveSidecarLogin(startSidecarCmd, nil, false); err == nil ||
		!strings.Contains(err.Error(), sidecarFreeStandaloneEnv) {
		t.Fatalf("a value that is not a boolean was accepted: err=%v", err)
	}
}

// --free-standalone is internal: --help does not list it.
func TestFreeStandaloneIsHidden(t *testing.T) {
	f := startSidecarCmd.Flags().Lookup("free-standalone")
	if f == nil || !f.Hidden {
		t.Fatalf("--free-standalone is %+v, want a hidden flag", f)
	}
	if strings.Contains(startSidecarCmd.Flags().FlagUsages(), "free-standalone") {
		t.Fatal("--help lists --free-standalone")
	}
}

// mustSaveSidecarLogin saves a login for a test, failing it on the real cause.
func mustSaveSidecarLogin(t *testing.T, dir string, l savedSidecarLogin) {
	t.Helper()
	if _, err := saveSidecarLogin(dir, l); err != nil {
		t.Fatalf("saving the login for %s: %v", l.URL, err)
	}
}

// bareRunWith resolves a bare run against a state dir that holds logins.
func bareRunWith(t *testing.T, logins ...savedSidecarLogin) (*savedSidecarLogin, error) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(SidecarStateDirEnv, dir)
	t.Setenv("HOOP_CONTROL_PLANE_URL", "")
	t.Setenv("HOOP_SIDECAR_TOKEN", "")
	t.Setenv("HOOP_SIDECAR_IDENTITY_TYPE", "")
	t.Setenv("HOOP_LICENSE", "")
	defer func(c, tok string) { sidecarConfigFlag, sidecarTokenFlag = c, tok }(sidecarConfigFlag, sidecarTokenFlag)
	sidecarConfigFlag = ""
	for _, l := range logins {
		mustSaveSidecarLogin(t, dir, l)
	}
	login, _, choose, err := resolveSidecarLogin(startSidecarCmd, nil, true)
	if choose {
		t.Fatal("a bare run with a saved login asked to choose a plane")
	}
	return login, err
}

// A bare run that names no plane uses the one login saved here, even when it
// is an Enterprise plane's.
func TestABareRunUsesTheOnlySavedEnterpriseLogin(t *testing.T) {
	login, err := bareRunWith(t, savedSidecarLogin{URL: "https://cp.example.com", Token: "hsc_enterprise", Name: "edge"})
	if err != nil || login == nil || login.Token != "hsc_enterprise" {
		t.Fatalf("login=%+v err=%v; want the Enterprise login", login, err)
	}
	if got := os.Getenv("HOOP_CONTROL_PLANE_URL"); got != "https://cp.example.com" {
		t.Fatalf("the plane is %q, want the saved Enterprise one", got)
	}
}

// The file a bare run boots again now holds a license: the run is licensed
// and runs that file on its own, without the saved token.
func TestABareRunWhoseFileGainedALicenseRunsItAlone(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "sidecar.yaml")
	if err := os.WriteFile(cfg, []byte("license: lic-test\nlisteners:\n  - name: db\n    protocol: postgres\n"+
		"    listen: 127.0.0.1:15432\n    upstream: 127.0.0.1:5432\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	login, err := bareRunWith(t, savedSidecarLogin{URL: DefaultSidecarControlPlaneURL, Token: "hsc_free", Config: cfg})
	if err != nil || login != nil {
		t.Fatalf("login=%+v err=%v; want no login", login, err)
	}
	if sidecarTokenFlag != "" {
		t.Fatal("the saved token was injected into a licensed run")
	}
}

// A licensed file that names a plane still connects to it: the login stays.
func TestABareRunKeepsTheLoginOfALicensedFilesPlane(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "sidecar.yaml")
	if err := os.WriteFile(cfg, []byte("license: lic-test\ncontrol_plane_url: https://cp.example.com\n"+
		"listeners:\n  - name: db\n    protocol: postgres\n    listen: 127.0.0.1:15432\n    upstream: 127.0.0.1:5432\n"),
		0o600); err != nil {
		t.Fatal(err)
	}
	login, err := bareRunWith(t, savedSidecarLogin{URL: "https://cp.example.com", Token: "hsc_enterprise", Config: cfg})
	if err != nil || login == nil || login.Token != "hsc_enterprise" {
		t.Fatalf("login=%+v err=%v; want the plane's login kept", login, err)
	}
	if got := os.Getenv("HOOP_CONTROL_PLANE_URL"); got != "https://cp.example.com" {
		t.Fatalf("the plane is %q, want the file's", got)
	}
}

// The file a bare run boots again now names another plane: the run uses that
// plane's login, not the one that remembered the file.
func TestABareRunFollowsTheFilesNewPlane(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "sidecar.yaml")
	if err := os.WriteFile(cfg, []byte("control_plane_url: https://cp.example.com\nlisteners:\n  - name: db\n"+
		"    protocol: postgres\n    listen: 127.0.0.1:15432\n    upstream: 127.0.0.1:5432\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	login, err := bareRunWith(t,
		savedSidecarLogin{URL: DefaultSidecarControlPlaneURL, Token: "hsc_hosted", Config: cfg},
		savedSidecarLogin{URL: "https://cp.example.com", Token: "hsc_enterprise"})
	if err != nil || login == nil || login.Token != "hsc_enterprise" {
		t.Fatalf("login=%+v err=%v; want the login of the plane the file names", login, err)
	}
	if got := os.Getenv("HOOP_CONTROL_PLANE_URL"); got != "https://cp.example.com" {
		t.Fatalf("the plane is %q, want the file's", got)
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
