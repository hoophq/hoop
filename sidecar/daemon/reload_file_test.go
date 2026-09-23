package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/license/licensetest"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// testFileReloader builds a reloader the way a STANDALONE Run does: no
// control plane, the config file as the owner, its stat and document seeded
// as the compare. It returns the file's path so a case edits it.
func testFileReloader(t *testing.T, raw string) (*reloader, string, *bytes.Buffer) {
	t.Helper()
	path := writeConfig(t, raw)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg.configPath = path
	cfg.load = LoadConfig
	cfg.lic = ResolveLicense("", cfg.License)

	lanes, err := buildLanes(cfg, nil, nil)
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	servers := map[string]*proxy.Server{}
	for _, ln := range lanes {
		srv, serr := buildServer(ln, cfg.Audit, nil, slog.Default())
		if serr != nil {
			t.Fatalf("buildServer: %v", serr)
		}
		servers[ln.name] = srv
	}
	view := &atomic.Pointer[laneState]{}
	view.Store(&laneState{lanes: lanes})
	rl, err := newReloader(cfg, lanes, servers, nil, nil, view,
		newLicenseState(cfg.lic, cfg.dependsOnLicense()))
	if err != nil {
		t.Fatalf("newReloader: %v", err)
	}
	if rl.planeOwned {
		t.Fatal("a reloader built without a plane reports one")
	}
	var buf bytes.Buffer
	return rl, path, &buf
}

// editFile replaces the file and moves its mtime a second ahead of the
// previous one, so the poll's stat compare sees the edit regardless of the
// filesystem's timestamp resolution.
func editFile(t *testing.T, path, body string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	next := st.ModTime().Add(time.Second)
	if err := os.Chtimes(path, next, next); err != nil {
		t.Fatal(err)
	}
}

func pollWith(rl *reloader, buf *bytes.Buffer) {
	rl.pollFile(slog.New(slog.NewTextHandler(buf, nil)))
}

// The reason this source exists: a rule edited in the file reaches the lane
// on the next tick, with no restart and therefore no dropped connection.
func TestAStandaloneRuleEditAppliesOnTheNextTick(t *testing.T) {
	rl, path, buf := testFileReloader(t, reloadBase)

	editFile(t, path, editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["truncate"]`))
	pollWith(rl, buf)
	if rl.gen != 1 {
		t.Fatalf("generation = %d, want 1; log:\n%s", rl.gen, buf)
	}
	if !strings.Contains(buf.String(), "config file configuration applied") {
		t.Errorf("the applied line does not name the file as the owner:\n%s", buf)
	}
	if st := rl.view.Load(); st.gen != 1 || len(st.lanes) != 1 {
		t.Errorf("the admin view was not published: gen %d, %d lanes", st.gen, len(st.lanes))
	}
}

// A tick that finds the file untouched must do nothing, and so must a touch
// that moves the mtime without changing the document: neither is an edit,
// and a generation that swapped nothing would send an operator reading the
// log after a phantom.
func TestAnUntouchedFileTicksSilently(t *testing.T) {
	rl, path, buf := testFileReloader(t, reloadBase)

	pollWith(rl, buf)
	editFile(t, path, reloadBase)
	pollWith(rl, buf)
	if rl.gen != 0 {
		t.Fatalf("generation = %d, want 0", rl.gen)
	}
	if buf.Len() != 0 {
		t.Errorf("an untouched file logged:\n%s", buf)
	}
}

// The boundary is the same as the plane's: a listener re-pointed in the file
// keeps today's restart log, and the running lane keeps its rules.
func TestAStandaloneTopologyEditKeepsTheRestartPath(t *testing.T) {
	rl, path, buf := testFileReloader(t, reloadBase)

	editFile(t, path, editJSON(t, reloadBase, `"upstream": "h:5432"`, `"upstream": "other:5432"`))
	pollWith(rl, buf)
	if rl.gen != 0 {
		t.Fatalf("generation = %d, want 0", rl.gen)
	}
	if !strings.Contains(buf.String(), "config file changed the configuration beyond the rules; restart to apply it") {
		t.Errorf("no restart line:\n%s", buf)
	}
	// Said once: the document is handled, and the next tick over the same
	// file adds nothing.
	before := buf.Len()
	rl.filePending = true
	pollWith(rl, buf)
	if buf.Len() != before {
		t.Errorf("the restart line repeated:\n%s", buf)
	}
}

// A file caught mid-write, or left broken, keeps the running rules. It is
// retried on the next tick without waiting for another edit, so a write that
// finishes after the read still lands; and it warns once, not once per tick.
func TestABrokenFileKeepsTheRulesAndIsRetried(t *testing.T) {
	rl, path, buf := testFileReloader(t, reloadBase)

	editFile(t, path, `{"listeners": [`)
	pollWith(rl, buf)
	if rl.gen != 0 || !rl.filePending {
		t.Fatalf("gen %d pending %v; want 0 and a pending retry; log:\n%s", rl.gen, rl.filePending, buf)
	}
	if !strings.Contains(buf.String(), "does not load; keeping the running rules") {
		t.Fatalf("the broken file was not reported:\n%s", buf)
	}
	pollWith(rl, buf)
	if n := strings.Count(buf.String(), "does not load"); n != 1 {
		t.Errorf("the broken file was reported %d times, want once:\n%s", n, buf)
	}

	editFile(t, path, editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["truncate"]`))
	pollWith(rl, buf)
	if rl.gen != 1 || rl.filePending {
		t.Fatalf("gen %d pending %v after the fix; want 1 and no retry; log:\n%s", rl.gen, rl.filePending, buf)
	}
}

// load_from_disk is the plane's word, not the file's. Startup refuses a file
// that writes it; a running process refuses the edit the same way and keeps
// serving.
func TestAFileWritingLoadFromDiskIsRefused(t *testing.T) {
	rl, path, buf := testFileReloader(t, reloadBase)

	editFile(t, path, editJSON(t, reloadBase, `"log_level": "info"`, `"log_level": "info", "load_from_disk": true`))
	pollWith(rl, buf)
	if rl.gen != 0 {
		t.Fatalf("generation = %d, want 0", rl.gen)
	}
	if !strings.Contains(buf.String(), "is not a config file key; keeping the running rules") {
		t.Errorf("the key was not refused by name:\n%s", buf)
	}
}

// SIGHUP is the immediate path: the same read a tick runs, now.
func TestSIGHUPRereadsTheFile(t *testing.T) {
	rl, path, buf := testFileReloader(t, reloadBase)
	rl.fileEvery = time.Hour

	// Registered here first so the signal has a handler even if it lands
	// before watchFile's own Notify runs; unhandled, SIGHUP's default action
	// ends the test binary.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGHUP)
	defer signal.Stop(guard)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		rl.watchFile(ctx, slog.New(slog.NewTextHandler(buf, nil)))
	}()

	editFile(t, path, editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["truncate"]`))
	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := self.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for rl.view.Load().gen != 1 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("SIGHUP did not apply the edit; log:\n%s", buf)
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

// The file's `license` key is the lowest local source. While HOOP_LICENSE
// holds the document in force, editing the key changes nothing -- on a
// reload as on a restart -- and the process says so once.
func TestAFileLicenseEditIsOutrankedByTheEnvVar(t *testing.T) {
	t.Setenv(license.EnvVar, licensetest.Document(t, licensetest.Enterprise()))
	rl, path, buf := testFileReloader(t, reloadBase)
	if src := rl.lic.get().Source; src != license.EnvVar {
		t.Fatalf("test bug: license source = %q, want %s", src, license.EnvVar)
	}

	editFile(t, path, withLicense(t, reloadBase, "not a license"))
	pollWith(rl, buf)
	if src := rl.lic.get().Source; src != license.EnvVar {
		t.Errorf("license source = %q after a file edit, want %s", src, license.EnvVar)
	}
	if !strings.Contains(buf.String(), "outranks it") {
		t.Errorf("the precedence was not explained:\n%s", buf)
	}
}

// With nothing outranking it, the key licenses a running process: a renewal
// dropped into the file must not cost an outage.
func TestAFileLicenseEditLicensesAnUnlicensedProcess(t *testing.T) {
	t.Setenv(license.EnvVar, "")
	rl, path, buf := testFileReloader(t, reloadBase)
	if st := rl.lic.get().State(); st != license.StateMissing {
		t.Fatalf("test bug: license state = %q, want missing", st)
	}

	editFile(t, path, withLicense(t, reloadBase, licensetest.Document(t, licensetest.Enterprise())))
	pollWith(rl, buf)
	cur := rl.lic.get()
	if cur.State() != license.StateValid || cur.Source != fileLicenseSource {
		t.Errorf("license = %q from %q, want valid from %s; log:\n%s",
			cur.State(), cur.Source, fileLicenseSource, buf)
	}
}

// The file starts naming a control plane. That is a change of owner, which
// only startup can make: the rules must NOT swap under a log line claiming
// the document applied, because nothing was fetched from the plane.
func TestAFileNamingAControlPlaneKeepsTheRestartPath(t *testing.T) {
	rl, path, buf := testFileReloader(t, reloadBase)

	drifted := editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["truncate"]`)
	drifted = editJSON(t, drifted, `"log_level": "info"`, `"log_level": "info", "control_plane_url": "http://plane"`)
	editFile(t, path, drifted)
	pollWith(rl, buf)
	if rl.gen != 0 {
		t.Fatalf("generation = %d, want 0: the rules swapped under a new owner; log:\n%s", rl.gen, buf)
	}
	if !strings.Contains(buf.String(), "names a control plane; restart to connect to it") {
		t.Errorf("no restart line:\n%s", buf)
	}
	if strings.Contains(buf.String(), "configuration applied") {
		t.Errorf("the document was reported applied:\n%s", buf)
	}
	before := buf.Len()
	rl.filePending = true
	pollWith(rl, buf)
	if buf.Len() != before {
		t.Errorf("the restart line repeated:\n%s", buf)
	}
}

// A renewal dropped onto the same mount: the config's `license` key is a
// path and does not change, the file behind it does. The string compare
// cannot see that, so a forced reload re-resolves the path and adopts the
// new document.
func TestAReplacedLicenseFileBehindTheSamePathIsAdopted(t *testing.T) {
	t.Setenv(license.EnvVar, "")
	licPath := filepath.Join(t.TempDir(), "license.json")
	first := licensetest.Document(t, licensetest.Enterprise())
	if err := os.WriteFile(licPath, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}
	rl, _, buf := testFileReloader(t, withLicense(t, reloadBase, licPath))
	was := rl.lic.get()
	if was.State() != license.StateValid || was.Source != fileLicenseSource {
		t.Fatalf("test bug: license = %q from %q", was.State(), was.Source)
	}

	// licensetest signs under a fresh trust root each time, so this is a
	// genuinely different document with a different signature.
	second := licensetest.Document(t, licensetest.Enterprise())
	if err := os.WriteFile(licPath, []byte(second), 0o600); err != nil {
		t.Fatal(err)
	}
	// The config file did not move: a tick sees nothing.
	pollWith(rl, buf)
	if got := rl.lic.get(); got.License.Signature != was.License.Signature {
		t.Fatal("a tick over an untouched config re-read the license; only a forced reload should")
	}
	// SIGHUP's path.
	rl.lastHandled = nil
	rl.reloadFile(slog.New(slog.NewTextHandler(buf, nil)))
	got := rl.lic.get()
	if got.State() != license.StateValid {
		t.Fatalf("license = %q after the forced reload, want valid; log:\n%s", got.State(), buf)
	}
	if got.License.Signature == was.License.Signature {
		t.Errorf("the running license is still the replaced document; log:\n%s", buf)
	}
}

// A narrower license arrives over rules that do not fit: overCap is set and
// the watchdog will stop the relay. The operator trims the rules before that
// tick. The compliant generation must clear the flag, or the watchdog stops
// a relay whose rules its license now covers.
func TestACompliantReloadClearsOverCap(t *testing.T) {
	t.Setenv(license.EnvVar, "")
	overCap := editJSON(t, reloadBase,
		`"rules": [
      {"name": "r0", "type": "deny_words_list", "words": ["drop table"]}
    ]`,
		`"rules": [
      {"name": "r0", "type": "deny_words_list", "words": ["drop table"]},
      {"name": "r1", "type": "deny_words_list", "words": ["truncate"]},
      {"name": "r2", "type": "deny_words_list", "words": ["delete from"]},
      {"name": "r3", "type": "deny_words_list", "words": ["alter table"]},
      {"name": "r4", "type": "deny_words_list", "words": ["grant"]},
      {"name": "r5", "type": "deny_words_list", "words": ["revoke"]}
    ]`)
	doc := licensetest.Document(t, licensetest.Enterprise())
	rl, path, buf := testFileReloader(t, withLicense(t, overCap, doc))

	// The license key is removed while six rules stay: refused, and the
	// relay is told to stop.
	editFile(t, path, overCap)
	pollWith(rl, buf)
	if !rl.lic.overCap.Load() {
		t.Fatalf("overCap not set after the license was removed over six rules; log:\n%s", buf)
	}
	if rl.gen != 0 {
		t.Fatalf("generation = %d, want 0", rl.gen)
	}

	// Before the watchdog ticks, the rules are trimmed to fit the free tier.
	editFile(t, path, reloadBase)
	pollWith(rl, buf)
	if rl.gen != 1 {
		t.Fatalf("generation = %d, want 1: the compliant edit did not apply; log:\n%s", rl.gen, buf)
	}
	if rl.lic.overCap.Load() {
		t.Error("overCap still set after a generation the license covers; the watchdog would stop a compliant relay")
	}
}
