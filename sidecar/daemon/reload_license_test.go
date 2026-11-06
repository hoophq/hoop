package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/license/licensetest"
)

// overCapBase is reloadBase with two guardrail rules, one past the free
// tier, so the running rules need a license that lifts the guardrail cap.
const overCapBase = `{
  "listeners": [{
    "name": "appdb", "protocol": "postgres",
    "listen": "127.0.0.1:0", "upstream": "h:5432",
    "guardrails": {"mode": "enforce", "rules": [
      {"name": "r0", "type": "deny_words_list", "words": ["drop table"]},
      {"name": "r1", "type": "deny_words_list", "words": ["truncate"]}
    ]}
  }],
  "audit": {"file": "-"},
  "log_level": "info"
}`

func writeLicenseFile(t *testing.T, doc string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "license.json")
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// startedWith builds a standalone reloader whose license came from source,
// which names licPath.
func startedWith(t *testing.T, raw, source, licPath string) (*reloader, string, *bytes.Buffer) {
	t.Helper()
	t.Setenv(license.EnvVar, "")
	flag := ""
	switch source {
	case fileLicenseSource:
		raw = withLicense(t, raw, licPath)
	case flagLicenseSource:
		flag = licPath
	case license.EnvVar:
		t.Setenv(license.EnvVar, licPath)
	}
	rl, cfgPath, buf := testFileReloaderWith(t, raw, flag)
	if got := rl.lic.get(); got.State() != license.StateValid || got.Source != source {
		t.Fatalf("test bug: license = %q from %q, want valid from %s", got.State(), got.Source, source)
	}
	return rl, cfgPath, buf
}

// A renewal dropped onto the mounted path of whichever source is in force is
// in force on the next tick, with no restart and no config edit.
func TestAReplacedLicenseFileIsAdoptedOnTheNextTick(t *testing.T) {
	for _, source := range []string{fileLicenseSource, flagLicenseSource, license.EnvVar} {
		t.Run(source, func(t *testing.T) {
			signer := licensetest.NewSigner(t)
			licPath := writeLicenseFile(t, signer.Document(licensetest.Enterprise()))
			rl, _, buf := startedWith(t, overCapBase, source, licPath)
			was := rl.lic.get()

			pollWith(rl, buf)
			if !sameLicenseDoc(rl.lic.get(), was) {
				t.Fatalf("a tick over an untouched license file changed the license; log:\n%s", buf)
			}

			editFile(t, licPath, signer.Document(licensetest.Enterprise()))
			pollWith(rl, buf)
			got := rl.lic.get()
			if got.State() != license.StateValid || sameLicenseDoc(got, was) {
				t.Fatalf("license = %q, replaced=%v; want the renewal in force; log:\n%s",
					got.State(), !sameLicenseDoc(got, was), buf)
			}
			if got.Source != source {
				t.Errorf("source = %q, want %q", got.Source, source)
			}
			if rl.lic.overCap.Load() {
				t.Errorf("a renewal that covers the rules set overCap; log:\n%s", buf)
			}
		})
	}
}

// The owner replaced the license with one that does not cover the running
// rules. It is in force, and the relay stops so the restart refuses the
// config by name: a rule is never dropped from a live proxy to fit.
func TestAReplacedLicenseFileThatNoLongerCoversTheRulesStopsTheRelay(t *testing.T) {
	signer := licensetest.NewSigner(t)
	licPath := writeLicenseFile(t, signer.Document(licensetest.Enterprise()))
	rl, _, buf := startedWith(t, overCapBase, flagLicenseSource, licPath)

	editFile(t, licPath, signer.Document(licensetest.Enterprise(license.FeatureDataMasking)))
	pollWith(rl, buf)

	if got := rl.lic.get(); got.Allows(license.FeatureGuardrails) {
		t.Fatalf("the narrower license is not in force; log:\n%s", buf)
	}
	if !rl.lic.overCap.Load() {
		t.Errorf("overCap not set over rules the new license does not cover; log:\n%s", buf)
	}
}

// A file caught mid-write, or replaced with garbage, keeps the license in
// force and warns once. Fixed, it is adopted without another trigger.
func TestABrokenLicenseFileKeepsTheLicenseInUse(t *testing.T) {
	signer := licensetest.NewSigner(t)
	licPath := writeLicenseFile(t, signer.Document(licensetest.Enterprise()))
	rl, _, buf := startedWith(t, reloadBase, license.EnvVar, licPath)
	was := rl.lic.get()

	editFile(t, licPath, `{"payload":`)
	pollWith(rl, buf)
	pollWith(rl, buf)
	if !sameLicenseDoc(rl.lic.get(), was) {
		t.Fatalf("a broken file replaced the license in use; log:\n%s", buf)
	}
	if n := strings.Count(buf.String(), "the license file does not load"); n != 1 {
		t.Errorf("warned %d times, want once; log:\n%s", n, buf)
	}

	editFile(t, licPath, signer.Document(licensetest.Enterprise()))
	pollWith(rl, buf)
	if got := rl.lic.get(); got.State() != license.StateValid || sameLicenseDoc(got, was) {
		t.Errorf("the fixed file was not adopted; log:\n%s", buf)
	}
}

// Rules that needed a wider license were refused; the wider license then
// lands in the file. The refused config applies on the next tick without
// another edit.
func TestARefusedConfigAppliesOnceTheLicenseFileCoversIt(t *testing.T) {
	signer := licensetest.NewSigner(t)
	licPath := writeLicenseFile(t, signer.Document(licensetest.Enterprise(license.FeatureDataMasking)))
	rl, cfgPath, buf := startedWith(t, reloadBase, flagLicenseSource, licPath)

	editFile(t, cfgPath, overCapBase)
	editFile(t, licPath, signer.Document(licensetest.Enterprise()))
	pollWith(rl, buf)
	pollWith(rl, buf)
	if rl.gen != 1 {
		t.Fatalf("generation = %d, want the refused config applied; log:\n%s", rl.gen, buf)
	}
}

// legacyPlaneReloader builds a reloader under a control plane that does not
// manage the license (a gateway older than the feature), where the config
// key raw carries stays a license source.
func legacyPlaneReloader(t *testing.T, raw string) *reloader {
	t.Helper()
	cfg, err := LoadConfigBytes([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	cfg.lic = ResolveLicense("", cfg.License)
	cfg.cp = &controlPlane{url: "http://plane", cred: tokenCredential("hsc_x"), lastRaw: []byte(raw)}
	return reloaderFor(t, cfg)
}

// reloaderFor builds a reloader over cfg as it stands, with no swap targets:
// enough for the license paths, which swap no lane.
func reloaderFor(t *testing.T, cfg *Config) *reloader {
	t.Helper()
	lanes, err := buildLanes(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	view := &atomic.Pointer[laneState]{}
	view.Store(&laneState{lanes: lanes})
	rl, err := newReloader(cfg, lanes, map[string]ruleSwapper{}, nil, nil, view,
		newLicenseState(cfg.lic, cfg.dependsOnLicense()))
	if err != nil {
		t.Fatal(err)
	}
	return rl
}

// A legacy plane whose document starts carrying its own license: that license
// is in force, and the local file that backed the one before no longer
// replaces it.
func TestALegacyPlanesOwnLicenseStopsTheLocalFileWatch(t *testing.T) {
	t.Setenv(license.EnvVar, "")
	signer := licensetest.NewSigner(t)
	licPath := writeLicenseFile(t, signer.Document(licensetest.Enterprise()))
	rl := legacyPlaneReloader(t, withLicense(t, reloadBase, licPath))
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	planeDoc := withLicense(t, reloadBase, signer.Document(licensetest.Enterprise()))
	if out := rl.handleAnswer(log, []byte(planeDoc), false); out != reloadApplied {
		t.Fatalf("plane document = %v, want applied; log:\n%s", out, &buf)
	}
	plane := rl.lic.get()
	if plane.Source != PlaneLicenseSource {
		t.Fatalf("license from %q, want %s; log:\n%s", plane.Source, PlaneLicenseSource, &buf)
	}
	editFile(t, licPath, signer.Document(licensetest.Enterprise()))
	rl.pollLicense(log, true)
	if !sameLicenseDoc(rl.lic.get(), plane) {
		t.Errorf("the local file replaced the plane's license; log:\n%s", &buf)
	}
}

// An embedder that calls UseLicense after Setup runs under that license. The
// HOOP_LICENSE path Setup resolved first is not polled in its place.
func TestUseLicenseAfterSetupIsNotReplacedByTheEnvFile(t *testing.T) {
	signer := licensetest.NewSigner(t)
	envPath := writeLicenseFile(t, signer.Document(licensetest.Enterprise()))
	t.Setenv(license.EnvVar, envPath)
	cfg, err := LoadConfigBytes([]byte(reloadBase))
	if err != nil {
		t.Fatal(err)
	}
	cfg.lic = ResolveLicense("", cfg.License)
	cfg.licPinned = pinnedLicenseRef(cfg.lic, "")
	if err := cfg.UseLicense(license.Ref{Value: signer.Document(licensetest.Enterprise()), Source: "the embedder"}); err != nil {
		t.Fatal(err)
	}
	rl := reloaderFor(t, cfg)
	var buf bytes.Buffer
	rl.pollLicense(slog.New(slog.NewTextHandler(&buf, nil)), true)
	if got := rl.lic.get(); got.Source != "the embedder" {
		t.Errorf("license from %q after a poll, want the embedder's; log:\n%s", got.Source, &buf)
	}
}

// Under a legacy plane the file the config key names is still watched. When
// the gateway is upgraded and claims the license, local changes stop.
func TestALegacyPlaneStillWatchesTheConfigKeyFile(t *testing.T) {
	t.Setenv(license.EnvVar, "")
	signer := licensetest.NewSigner(t)
	licPath := writeLicenseFile(t, signer.Document(licensetest.Enterprise()))
	rl := legacyPlaneReloader(t, withLicense(t, reloadBase, licPath))
	was := rl.lic.get()
	var buf bytes.Buffer
	editFile(t, licPath, signer.Document(licensetest.Enterprise()))
	rl.pollLicense(slog.New(slog.NewTextHandler(&buf, nil)), false)
	if got := rl.lic.get(); sameLicenseDoc(got, was) || got.Source != fileLicenseSource {
		t.Errorf("license from %q, replaced=%v; want the renewal; log:\n%s", got.Source, !sameLicenseDoc(got, was), &buf)
	}

	// The gateway is upgraded and claims the license on a heartbeat, for an
	// organization that holds none: the plane's answer is in force at once,
	// as on a restart, and local changes stop.
	log := slog.New(slog.NewTextHandler(&buf, nil))
	if out := rl.handleAnswer(log, []byte(reloadBase), true); out != reloadApplied {
		t.Fatalf("claim = %v, want applied; log:\n%s", out, &buf)
	}
	if got := rl.lic.get(); got.State() != license.StateMissing {
		t.Fatalf("license = %q from %q after the plane claimed it with none, want missing; log:\n%s",
			got.State(), got.Source, &buf)
	}
	editFile(t, licPath, signer.Document(licensetest.Enterprise()))
	rl.pollLicense(log, true)
	if got := rl.lic.get(); got.State() != license.StateMissing {
		t.Errorf("a local file changed the license after the plane claimed it; log:\n%s", &buf)
	}
	if _, _, err := rl.installLicense(log, signer.Document(licensetest.Enterprise())); !errors.Is(err, errLicenseManaged) {
		t.Errorf("installLicense after the claim: %v, want the plane's refusal", err)
	}
}

// A plane that claims the very document already in force rotates nothing,
// but the license now reports the plane as its source.
func TestAClaimOfTheSameDocumentNamesThePlane(t *testing.T) {
	t.Setenv(license.EnvVar, "")
	doc := licensetest.Document(t, licensetest.Enterprise())
	rl := legacyPlaneReloader(t, withLicense(t, reloadBase, doc))
	var buf bytes.Buffer
	if out := rl.handleAnswer(slog.New(slog.NewTextHandler(&buf, nil)), []byte(withLicense(t, reloadBase, doc)), true); out != reloadApplied {
		t.Fatalf("claim = %v, want applied; log:\n%s", out, &buf)
	}
	if got := rl.lic.get(); got.State() != license.StateValid || got.Source != PlaneLicenseSource {
		t.Errorf("license = %q from %q, want valid from %s", got.State(), got.Source, PlaneLicenseSource)
	}
}

// A licensed sidecar whose config file starts naming a control plane is told,
// before the restart, that the plane's license replaces its own and that
// rules past the free tier need one there.
func TestNamingAControlPlaneWarnsTheLocalLicenseWillBeIgnored(t *testing.T) {
	signer := licensetest.NewSigner(t)
	licPath := writeLicenseFile(t, signer.Document(licensetest.Enterprise()))
	rl, cfgPath, buf := startedWith(t, overCapBase, license.EnvVar, licPath)

	editFile(t, cfgPath, editJSON(t, overCapBase, `"log_level": "info"`,
		`"log_level": "info", "control_plane_url": "https://cp.example.com"`))
	pollWith(rl, buf)
	for _, want := range []string{
		"the license from " + license.EnvVar + " will be ignored",
		"the sidecar will not start with them",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the warning does not say %q; log:\n%s", want, buf)
		}
	}
	before := buf.Len()
	pollWith(rl, buf)
	if buf.Len() != before {
		t.Errorf("the warning repeated on an untouched file:\n%s", buf.String()[before:])
	}
}

// An admin install is in force until the source in force changes. A rule
// edit and a SIGHUP over an unchanged license file keep it; a renewal on the
// flag's path replaces it, the same license a restart would pick.
func TestAnAdminInstallLastsUntilTheSourceInForceChanges(t *testing.T) {
	signer := licensetest.NewSigner(t)
	licPath := writeLicenseFile(t, signer.Document(licensetest.Enterprise()))
	rl, cfgPath, buf := startedWith(t, reloadBase, flagLicenseSource, licPath)
	log := slog.New(slog.NewTextHandler(buf, nil))

	if _, installed, err := rl.installLicense(log, signer.Document(licensetest.Enterprise())); err != nil || !installed {
		t.Fatalf("installLicense = %v, %v; want installed", installed, err)
	}
	admin := rl.lic.get()

	editFile(t, cfgPath, editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["truncate"]`))
	pollWith(rl, buf)
	rl.hangup(log)
	if got := rl.lic.get(); !sameLicenseDoc(got, admin) || got.Source != adminLicenseSource {
		t.Fatalf("license from %q after a rule edit and a SIGHUP, want the admin install; log:\n%s", got.Source, buf)
	}

	editFile(t, licPath, signer.Document(licensetest.Enterprise()))
	pollWith(rl, buf)
	if got := rl.lic.get(); got.Source != flagLicenseSource || sameLicenseDoc(got, admin) {
		t.Errorf("license from %q after the flag's file was renewed, want %s; log:\n%s",
			got.Source, flagLicenseSource, buf)
	}
}

// What the admin API refuses, and that a refusal leaves the license in force
// and the relay running.
func TestAnAdminInstallRefusesWhatItCannotRunUnder(t *testing.T) {
	signer := licensetest.NewSigner(t)
	licPath := writeLicenseFile(t, signer.Document(licensetest.Enterprise()))
	rl, _, buf := startedWith(t, overCapBase, fileLicenseSource, licPath)
	log := slog.New(slog.NewTextHandler(buf, nil))
	was := rl.lic.get()

	// A valid license on disk, named by path: the body must never make the
	// process open a file.
	other := writeLicenseFile(t, signer.Document(licensetest.Enterprise()))
	for name, body := range map[string]string{
		"a path":                     other,
		"an expired license":         signer.Document(licensetest.Expiring(-time.Hour)),
		"an unsigned license":        strings.Replace(signer.Document(licensetest.Enterprise()), `"signature":"`, `"signature":"x`, 1),
		"one that does not cover it": signer.Document(licensetest.Enterprise(license.FeatureDataMasking)),
		"nothing":                    "",
	} {
		if _, installed, err := rl.installLicense(log, body); !errors.Is(err, errLicenseRefused) || installed {
			t.Errorf("%s: installed=%v err=%v, want refused", name, installed, err)
		}
	}
	if !sameLicenseDoc(rl.lic.get(), was) || rl.lic.overCap.Load() {
		t.Errorf("a refused install changed the license or asked for a stop; log:\n%s", buf)
	}

	// The license already in force is an answer, not a refusal.
	doc, err := os.ReadFile(licPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, installed, err := rl.installLicense(log, string(doc)); err != nil || installed {
		t.Errorf("same document: installed=%v err=%v, want unchanged", installed, err)
	}
}

func TestAnAdminInstallIsRefusedUnderAPlaneThatOwnsTheLicense(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	_, _, err := rl.installLicense(slog.New(slog.NewTextHandler(buf, nil)),
		licensetest.Document(t, licensetest.Enterprise()))
	if !errors.Is(err, errLicenseManaged) {
		t.Fatalf("err = %v, want the plane's refusal", err)
	}
	if rl.lic.get().State() != license.StateMissing {
		t.Error("the plane's license was replaced")
	}
}

// The HTTP contract: off without a secret, closed without the right one, and
// the license report (never the document) on success.
func TestPutLicense(t *testing.T) {
	signer := licensetest.NewSigner(t)
	licPath := writeLicenseFile(t, signer.Document(licensetest.Enterprise()))
	rl, _, buf := startedWith(t, reloadBase, license.EnvVar, licPath)
	log := slog.New(slog.NewTextHandler(buf, nil))
	doc := signer.Document(licensetest.Enterprise())

	put := func(secret, auth, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/license", strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		licenseHandler(secret, rl, log).ServeHTTP(w, req)
		return w
	}
	for _, tc := range []struct {
		name, secret, auth, body string
		want                     int
	}{
		{"no secret configured", "", "Bearer s3cret", doc, http.StatusForbidden},
		{"no credential", "s3cret", "", doc, http.StatusUnauthorized},
		{"wrong secret", "s3cret", "Bearer nope", doc, http.StatusUnauthorized},
		{"a prefix of the secret", "s3cret", "Bearer s3cre", doc, http.StatusUnauthorized},
		{"too large", "s3cret", "Bearer s3cret", strings.Repeat(" ", maxLicenseBody+1), http.StatusRequestEntityTooLarge},
		{"not a license", "s3cret", "Bearer s3cret", "{}", http.StatusUnprocessableEntity},
	} {
		if w := put(tc.secret, tc.auth, tc.body); w.Code != tc.want {
			t.Errorf("%s: status %d, want %d: %s", tc.name, w.Code, tc.want, w.Body)
		}
	}
	if rl.lic.get().Source != license.EnvVar {
		t.Fatal("a refused request changed the license")
	}

	w := put("s3cret", "Bearer s3cret", doc)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "signature") {
		t.Errorf("the answer hands out the signature: %s", w.Body)
	}
	var got struct {
		Outcome string         `json:"outcome"`
		License map[string]any `json:"license"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "installed" || got.License["state"] != "valid" || got.License["source"] != adminLicenseSource {
		t.Errorf("answer = %+v, want installed, valid, from %s", got, adminLicenseSource)
	}
	if w := put("s3cret", "Bearer s3cret", doc); !strings.Contains(w.Body.String(), `"outcome":"unchanged"`) {
		t.Errorf("repeat: %s, want unchanged", w.Body)
	}
}
