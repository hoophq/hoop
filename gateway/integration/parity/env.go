//go:build integration && parity

package parity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/proto"
	_ "github.com/lib/pq"
)

const (
	adminEmail    = "parity-admin@parity.test"
	adminPassword = "parity-admin-pass-123"
	agentName     = "parity-agent"
	// The gateway listens for gRPC on this fixed port, so runs never overlap.
	grpcPort = 8010
	grpcURL  = "grpc://127.0.0.1:8010"
)

// RunSpec is how one run boots.
type RunSpec struct {
	Run Run
	// Mode is the `hoop start` subcommand: gateway or control-plane.
	Mode string
	// SidecarListeners turns beta.sidecar_listeners on after boot.
	SidecarListeners bool
	// Agent starts a real agent; only a gateway can accept one.
	Agent bool
	// Seed runs against the empty run database before the process boots.
	Seed func(dbURI string) error
}

// Env is a booted process under test.
type Env struct {
	Run      Run
	Mode     string
	API      string
	DBURI    string
	DB       *sql.DB
	Slack    *FakeSlack
	Token    string
	OrgID    string
	AgentID  string
	logDir   string
	procs    []*proc
	logFiles []*os.File
}

// Admin is a client authenticated as the first (admin) user.
func (e *Env) Admin() *Client {
	return &Client{base: e.API, header: http.Header{"Authorization": {"Bearer " + e.Token}}}
}

// Anonymous is a client with no credentials.
func (e *Env) Anonymous() *Client { return &Client{base: e.API, header: http.Header{}} }

// Sidecar is a client authenticated as the sidecar holding token.
func (e *Env) Sidecar(token string) *Client {
	return &Client{base: e.API, header: http.Header{"Hoop-Sidecar-Token": {token}}}
}

// IsGateway reports whether the process serves the gateway data plane.
func (e *Env) IsGateway() bool { return e.Mode == "gateway" }

func startEnv(t *testing.T, h *harness, spec RunSpec) *Env {
	t.Helper()
	dbURI, err := h.createDatabase(string(spec.Run))
	if err != nil {
		t.Fatalf("creating run database: %v", err)
	}
	if spec.Seed != nil {
		if err := spec.Seed(dbURI); err != nil {
			t.Fatalf("seeding run database: %v", err)
		}
	}
	db, err := sql.Open("postgres", dbURI)
	if err != nil {
		t.Fatalf("opening run database: %v", err)
	}
	slack, err := newFakeSlack()
	if err != nil {
		t.Fatalf("starting fake slack: %v", err)
	}
	port, err := freePort()
	if err != nil {
		t.Fatalf("picking http port: %v", err)
	}
	e := &Env{
		Run:    spec.Run,
		Mode:   spec.Mode,
		API:    fmt.Sprintf("http://127.0.0.1:%d", port),
		DBURI:  dbURI,
		DB:     db,
		Slack:  slack,
		logDir: filepath.Join(h.logDir, string(spec.Run)),
	}
	t.Cleanup(func() { e.stop(t) })
	if err := os.MkdirAll(e.logDir, 0o755); err != nil {
		t.Fatalf("creating log dir: %v", err)
	}
	if spec.Mode == "gateway" {
		if err := portFree(grpcPort); err != nil {
			t.Fatalf("the gateway needs port %d for gRPC: %v", grpcPort, err)
		}
	}
	home := t.TempDir()
	e.start(t, h.bin, "server", []string{"start", spec.Mode}, append(baseEnv(home),
		"POSTGRES_DB_URI="+dbURI,
		"API_URL="+e.API,
		"GRPC_URL="+grpcURL,
		fmt.Sprintf("PORT=%d", port),
		"AUTH_METHOD=local",
		"SLACK_API_URL="+slack.URL(),
		"PLUGIN_AUDIT_PATH="+filepath.Join(home, "sessions"),
		"PLUGIN_INDEX_PATH="+filepath.Join(home, "sessions", "indexes"),
	))
	e.waitHealthy(t)
	e.Token = e.registerAdmin(t)
	e.OrgID = e.orgID(t)
	e.installLicense(t, h)
	if spec.SidecarListeners {
		e.mustAPI(t, http.MethodPut, "/feature-flags/beta.sidecar_listeners", map[string]any{"enabled": true}, http.StatusOK)
	}
	if spec.Agent {
		e.startAgent(t, h.bin, home)
	}
	return e
}

func baseEnv(home string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"DO_NOT_TRACK=1",
		"LOG_LEVEL=info",
		"LOG_ENCODING=console",
	}
}

func (e *Env) start(t *testing.T, bin, name string, args, env []string) {
	t.Helper()
	logf, err := os.Create(filepath.Join(e.logDir, name+".log"))
	if err != nil {
		t.Fatalf("creating %s log: %v", name, err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting %s: %v", name, err)
	}
	p := &proc{cmd: cmd, exited: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(p.exited) }()
	e.procs = append(e.procs, p)
	e.logFiles = append(e.logFiles, logf)
}

type proc struct {
	cmd    *exec.Cmd
	exited chan struct{}
}

func (p *proc) running() bool {
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

// Shutdown terminates every process, newest first, so the agent leaves
// before its gateway. Later checks on the run can then reach only the
// database. Calling it again does nothing.
func (e *Env) Shutdown() {
	for i := len(e.procs) - 1; i >= 0; i-- {
		p := e.procs[i]
		if !p.running() {
			continue
		}
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-p.exited:
		case <-time.After(20 * time.Second):
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
			<-p.exited
		}
	}
}

func (e *Env) stop(t *testing.T) {
	e.Shutdown()
	for _, f := range e.logFiles {
		_ = f.Close()
	}
	if e.Slack != nil {
		e.Slack.Close()
	}
	if e.DB != nil {
		_ = e.DB.Close()
	}
	if t.Failed() {
		t.Logf("process logs kept in %s", e.logDir)
	}
}

func (e *Env) waitHealthy(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if !e.procs[0].running() {
			t.Fatalf("%s exited during boot; see %s", e.Mode, e.logDir)
		}
		r, err := e.Anonymous().do(http.MethodGet, "/healthz", nil)
		if err == nil && r.Status == http.StatusOK {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s not healthy within 3m; see %s", e.Mode, e.logDir)
}

func (e *Env) registerAdmin(t *testing.T) string {
	t.Helper()
	r, err := e.Anonymous().do(http.MethodPost, "/localauth/register", map[string]string{
		"email": adminEmail, "password": adminPassword, "name": "Parity Admin",
	})
	if err != nil || r.Status != http.StatusCreated {
		t.Fatalf("registering admin: status=%d err=%v body=%s", r.Status, err, truncate(r.Body))
	}
	token := r.Header.Get("Token")
	if token == "" {
		t.Fatal("registering admin: no Token header")
	}
	return token
}

func (e *Env) orgID(t *testing.T) string {
	t.Helper()
	var id string
	if err := e.DB.QueryRow(`SELECT id FROM private.orgs WHERE name = $1`, proto.DefaultOrgName).Scan(&id); err != nil {
		t.Fatalf("reading default org id: %v", err)
	}
	return id
}

// installLicense uploads an Enterprise license for 127.0.0.1, signed with the
// key the binary under test was built to trust.
func (e *Env) installLicense(t *testing.T, h *harness) {
	t.Helper()
	l, err := license.Sign(h.licenseKey, license.EnterpriseType, "parity acceptance", []string{"127.0.0.1"}, nil, 24*time.Hour)
	if err != nil {
		t.Fatalf("signing license: %v", err)
	}
	e.mustAPI(t, http.MethodPut, "/orgs/license", l, http.StatusNoContent)
}

func (e *Env) startAgent(t *testing.T, bin, home string) {
	t.Helper()
	var created struct {
		Token string `json:"token"`
	}
	r := e.mustAPI(t, http.MethodPost, "/agents", map[string]string{"name": agentName, "mode": "standard"}, http.StatusCreated)
	if err := json.Unmarshal(r.Body, &created); err != nil || created.Token == "" {
		t.Fatalf("decoding agent key: %v body=%s", err, truncate(r.Body))
	}
	e.start(t, bin, "agent", []string{"start", "agent"}, append(baseEnv(home), "HOOP_KEY="+created.Token))
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		r := e.mustAPI(t, http.MethodGet, "/agents", nil, http.StatusOK)
		var agents []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(r.Body, &agents); err != nil {
			t.Fatalf("decoding agents: %v", err)
		}
		for _, a := range agents {
			if a.Name == agentName && a.Status == "CONNECTED" {
				e.AgentID = a.ID
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("agent not CONNECTED within 90s; see %s", e.logDir)
}

// mustAPI is the boot-time call: it fails the run, not a check.
func (e *Env) mustAPI(t *testing.T, method, path string, body any, want int) Resp {
	t.Helper()
	r, err := e.Admin().do(method, path, body)
	if err != nil || r.Status != want {
		t.Fatalf("%s %s: status=%d want=%d err=%v body=%s", method, path, r.Status, want, err, truncate(r.Body))
	}
	return r
}

// portFree reports an error when something already listens on port.
func portFree(port int) error {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	return l.Close()
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// waitFor polls cond until it holds or the timeout passes.
func waitFor(ctx context.Context, timeout time.Duration, cond func() (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last error
	for {
		ok, err := cond()
		if ok {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			if last != nil {
				return fmt.Errorf("timed out: %w", last)
			}
			return fmt.Errorf("timed out")
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// sanitize keeps a run name usable as a database name.
func sanitize(s string) string { return strings.ReplaceAll(s, "-", "_") }
