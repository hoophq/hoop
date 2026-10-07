//go:build integration && parity

// Package parity is the daily acceptance scenario for the sidecar-in-gateway
// rollout (ENG-529). It builds the real hoop binary, boots it once per run
// against its own Postgres database, and drives it over HTTP only.
//
// Runs:
//   - gateway-flag-off: today's gateway. Nothing here may change.
//   - control-plane: today's control plane. Nothing here may change.
//   - gateway-flag-on: the gateway with beta.sidecar_listeners on. A check
//     the rollout has not delivered yet is pending on its ticket.
//
// MUST_NOT_BREAK.md lists every check; TestTheListMatchesTheChecks keeps the
// two in step.
package parity

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoop/gateway/integration/testutil"
)

var runs = []RunSpec{
	{Run: GatewayFlagOff, Mode: "gateway", Agent: true},
	{Run: ControlPlane, Mode: "control-plane"},
	{Run: GatewayFlagOn, Mode: "gateway", SidecarListeners: true, Agent: true},
}

type harness struct {
	bin        string
	licenseKey *rsa.PrivateKey
	logDir     string
	pg         *testutil.PostgresContainer
}

var h *harness

func TestMain(m *testing.M) {
	code, err := setup(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parity setup:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func setup(m *testing.M) (int, error) {
	logDir := os.Getenv("PARITY_LOG_DIR")
	if logDir == "" {
		dir, err := os.MkdirTemp("", "parity-logs-")
		if err != nil {
			return 0, err
		}
		logDir = dir
	} else if err := os.MkdirAll(logDir, 0o755); err != nil {
		return 0, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return 0, err
	}
	bin, err := buildHoop(logDir, &key.PublicKey)
	if err != nil {
		return 0, err
	}
	pg, err := testutil.StartPostgres(context.Background())
	if err != nil {
		return 0, err
	}
	defer func() { _ = pg.Terminate() }()
	h = &harness{bin: bin, licenseKey: key, logDir: logDir, pg: pg}
	code := m.Run()
	if err := rep.write(logDir); err != nil {
		fmt.Fprintln(os.Stderr, "parity report:", err)
		if code == 0 {
			code = 1
		}
	}
	fmt.Println("parity logs and report:", logDir)
	return code, nil
}

// trustRoots are the files that embed the license public key. The gateway
// verifies licenses with common/license; the served sidecar configuration is
// checked against the sidecar's own copy of the key.
var trustRoots = []string{
	"common/license/license.go",
	"sidecar/license/internal/trust/trust.go",
}

var pubKeyBlockRe = regexp.MustCompile("(?s)(var \\w+ = \\[\\]byte\\(`\\n)-----BEGIN PUBLIC KEY-----\\n.*?-----END PUBLIC KEY-----\\n(`\\))")

// buildHoop builds the CLI with one change: every license trust root trusts
// pub instead of the production key, so the harness can sign an Enterprise
// license. The overlay leaves every other file as shipped.
func buildHoop(dir string, pub *rsa.PublicKey) (string, error) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	replace := map[string]string{}
	for i, rel := range trustRoots {
		src := filepath.Join(root, rel)
		orig, err := os.ReadFile(src)
		if err != nil {
			return "", err
		}
		if n := len(pubKeyBlockRe.FindAllIndex(orig, -1)); n != 1 {
			return "", fmt.Errorf("%s: found %d embedded public keys, want 1; update trustRoots", src, n)
		}
		patched := pubKeyBlockRe.ReplaceAll(orig, []byte("${1}"+string(pemKey)+"${2}"))
		patchedPath := filepath.Join(dir, fmt.Sprintf("trust_overlay_%d.go", i))
		if err := os.WriteFile(patchedPath, patched, 0o644); err != nil {
			return "", err
		}
		replace[src] = patchedPath
	}
	overlay, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		return "", err
	}
	overlayPath := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0o644); err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "hoop")
	cmd := exec.Command("go", "build", "-overlay", overlayPath, "-o", bin, "./client")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %v\n%s", err, out)
	}
	return bin, nil
}

func (h *harness) createDatabase(run string) (string, error) {
	admin, err := sql.Open("postgres", h.pg.URI())
	if err != nil {
		return "", err
	}
	defer admin.Close()
	name := "parity_" + sanitize(run)
	if _, err := admin.Exec(`DROP DATABASE IF EXISTS ` + name); err != nil {
		return "", err
	}
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		return "", err
	}
	u, err := url.Parse(h.pg.URI())
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}

// selectedRuns honours PARITY_RUNS (comma separated) for local iteration.
func selectedRuns() []RunSpec {
	want := os.Getenv("PARITY_RUNS")
	if want == "" {
		return runs
	}
	var out []RunSpec
	for _, r := range runs {
		if slices.Contains(strings.Split(want, ","), string(r.Run)) {
			out = append(out, r)
		}
	}
	return out
}

func TestParity(t *testing.T) {
	for _, spec := range selectedRuns() {
		t.Run(string(spec.Run), func(t *testing.T) {
			env := startEnv(t, h, spec)
			for _, ck := range registry {
				exp, ok := ck.Runs[spec.Run]
				if !ok || !selectedCheck(ck.ID) {
					continue
				}
				t.Run(ck.ID, func(t *testing.T) { runCheck(t, env, ck, exp, rep) })
			}
		})
	}
}

// selectedCheck honours PARITY_CHECKS (comma separated id prefixes, such as
// "SC-,RV-01") for local iteration.
func selectedCheck(id string) bool {
	want := os.Getenv("PARITY_CHECKS")
	if want == "" {
		return true
	}
	for _, prefix := range strings.Split(want, ",") {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

var listRowRe = regexp.MustCompile(`^\|\s*([A-Z]{2,4}-\d{2})\s*\|`)

// listColumns is the run order of the MUST_NOT_BREAK.md table columns.
var listColumns = []Run{GatewayFlagOff, ControlPlane, GatewayFlagOn}

// listRow renders ck as its MUST_NOT_BREAK.md row.
func listRow(ck Check) string {
	cells := []string{ck.ID, ck.Title}
	for _, run := range listColumns {
		exp, ok := ck.Runs[run]
		switch {
		case !ok:
			cells = append(cells, "—")
		case exp.Pending != "":
			cells = append(cells, "pending "+exp.Pending)
		default:
			cells = append(cells, "must")
		}
	}
	return "| " + strings.Join(cells, " | ") + " |"
}

// TestTheListMatchesTheChecks keeps MUST_NOT_BREAK.md and the registry in
// step: every check has exactly its row, with its title and its must or
// pending state on each run, and every row is a check.
func TestTheListMatchesTheChecks(t *testing.T) {
	f, err := os.Open("MUST_NOT_BREAK.md")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	listed := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := listRowRe.FindStringSubmatch(sc.Text()); m != nil {
			if _, dup := listed[m[1]]; dup {
				t.Errorf("MUST_NOT_BREAK.md lists %s twice", m[1])
			}
			listed[m[1]] = sc.Text()
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	for _, ck := range registry {
		want := listRow(ck)
		got, ok := listed[ck.ID]
		switch {
		case !ok:
			t.Errorf("MUST_NOT_BREAK.md does not list %s; add:\n%s", ck.ID, want)
		case got != want:
			t.Errorf("MUST_NOT_BREAK.md row for %s is out of date:\n got: %s\nwant: %s", ck.ID, got, want)
		}
		delete(listed, ck.ID)
	}
	for id := range listed {
		t.Errorf("MUST_NOT_BREAK.md lists %s, which no check registers", id)
	}
}

type report struct {
	mu      sync.Mutex
	results []result
}

var rep = &report{}

func (r *report) add(res result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, res)
}

// write renders the results as markdown into dir/report.md and, on GitHub
// Actions, the job summary.
func (r *report) write(dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	counts := map[Outcome]int{}
	for _, res := range r.results {
		counts[res.Outcome]++
	}
	fmt.Fprintf(&b, "## Parity acceptance\n\n%d pass, %d fail, %d pending, %d pass-pending\n\n",
		counts[OutcomePass], counts[OutcomeFail], counts[OutcomePending], counts[OutcomeUnexpectedPass])
	b.WriteString("| Run | Check | Title | Result | Ticket | Detail |\n|---|---|---|---|---|---|\n")
	for _, res := range r.results {
		detail := strings.NewReplacer("|", "\\|", "\n", " ").Replace(res.Detail)
		if len(detail) > 200 {
			detail = detail[:200] + "..."
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", res.Run, res.ID, res.Title, res.Outcome, res.Ticket, detail)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte(b.String()), 0o644); err != nil {
		return err
	}
	if summary := os.Getenv("GITHUB_STEP_SUMMARY"); summary != "" {
		f, err := os.OpenFile(summary, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.WriteString(b.String()); err != nil {
			return err
		}
	}
	return nil
}
