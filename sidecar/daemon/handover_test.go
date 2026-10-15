package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// reportingPlane is a gateway that takes a sidecar's configuration only on an
// admin's action: a handshake carrying local_configuration for an empty row is
// answered load_from_disk, and the report is kept. It counts every PUT, so a
// test can prove the boot never imported.
type reportingPlane struct {
	mu      sync.Mutex
	srv     *httptest.Server
	reports []json.RawMessage
	puts    int
}

func newReportingPlane(t *testing.T) *reportingPlane {
	t.Helper()
	p := &reportingPlane{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == controlPlaneHandshakePath:
			var req handshakeRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			p.reports = append(p.reports, req.LocalConfiguration)
			if len(req.LocalConfiguration) == 0 {
				w.WriteHeader(http.StatusPreconditionFailed)
				_, _ = w.Write([]byte(`{"message":"no configuration is assigned to this sidecar"}`))
				return
			}
			w.Header().Set(LicenseManagedHeader, "true")
			_, _ = w.Write([]byte(`{"load_from_disk":true}`))
		case r.Method == http.MethodPut && r.URL.Path == controlPlaneConfigurationPath:
			_, _ = io.ReadAll(r.Body)
			p.puts++
			w.WriteHeader(http.StatusConflict)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

// The boot reports the file and runs it. The plane holds nothing and the
// sidecar pushes nothing: moving the configuration is an admin's action.
func TestTheBootReportsTheFileAndADiskAnswerNeverImports(t *testing.T) {
	plane := newReportingPlane(t)
	t.Setenv(ControlPlaneURLEnv, plane.srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_x")

	cfg, _, err := SetupWith(writeConfig(t, minimalConfig+`,"log_level":"debug","license":"FILE"}`), nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if plane.puts != 0 {
		t.Fatalf("imports = %d, want 0: the plane takes the file only on an admin's action", plane.puts)
	}
	if cfg.cp == nil || !cfg.cp.diskMode || len(cfg.Listeners) != 1 {
		t.Fatalf("the process does not run its file in disk mode: cp=%+v listeners=%d", cfg.cp, len(cfg.Listeners))
	}
	if len(plane.reports) != 1 {
		t.Fatalf("handshakes = %d, want 1", len(plane.reports))
	}
	report := string(plane.reports[0])
	if !strings.Contains(report, `"log_level":"debug"`) || !strings.Contains(report, `"upstream":"h:5432"`) {
		t.Errorf("the report lost a field: %s", report)
	}
	if strings.Contains(report, "control_plane_url") || strings.Contains(report, "license") {
		t.Errorf("the report leaks a file-side fact: %s", report)
	}
}

// A file with no listeners has nothing the plane could show or run, so it is
// not reported, and the empty plane's 412 stays the answer.
func TestAFileWithoutListenersIsNotReported(t *testing.T) {
	report, err := localReport(&Config{})
	if err != nil || report != nil {
		t.Fatalf("localReport = %s, %v; want nil, nil", report, err)
	}
	if report, err = localReport(nil); err != nil || report != nil {
		t.Fatalf("localReport(nil) = %s, %v; want nil, nil", report, err)
	}
}

// heartbeatReports runs the heartbeat against a plane that records each
// handshake's local_configuration and answers with answer.
func heartbeatReports(t *testing.T, rl *reloader, answer string) []json.RawMessage {
	t.Helper()
	var mu sync.Mutex
	var reports []json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var req handshakeRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		reports = append(reports, req.LocalConfiguration)
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()
	cp := &controlPlane{url: srv.URL, cred: tokenCredential("hsc_x"), every: time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	cp.heartbeat(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), rl)
	mu.Lock()
	defer mu.Unlock()
	return reports
}

// In disk mode every heartbeat reports the file document this process runs,
// not the file as it reads now: an edit it has not applied is never shown,
// and never adopted, as what runs.
func TestADiskModeHeartbeatReportsTheRunningFile(t *testing.T) {
	rl, _ := testReloader(t, reloadBase)
	rl.diskMode = true
	rl.lastHandled = []byte(`{"load_from_disk":true}`)
	rl.configPath = "sidecar.yaml"
	running := json.RawMessage(`{"listeners":[{"name":"running"}]}`)
	rl.runningFile = running
	// The disk holds an edit this process never applied.
	rl.load = func(string) (*Config, error) {
		return LoadConfigBytes([]byte(`{"listeners":[{"name":"edited","protocol":"postgres","listen":":1","upstream":"h:5432"}]}`))
	}

	reports := heartbeatReports(t, rl, `{"load_from_disk":true}`)
	if len(reports) == 0 {
		t.Fatal("no heartbeat ran")
	}
	for i, r := range reports {
		if string(r) != string(running) {
			t.Fatalf("heartbeat %d reported %s, want the running file %s", i, r, running)
		}
	}
}

// The file a disk-mode answer adopts becomes the report, once applied.
func TestAnAdoptedFileBecomesTheReport(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	rl.configPath = "sidecar.yaml"
	rl.load = func(string) (*Config, error) { return LoadConfigBytes([]byte(reloadBase)) }

	if out := rl.adoptFile(slog.New(slog.NewTextHandler(buf, nil)), ""); out != reloadApplied && out != reloadUnchanged {
		t.Fatalf("adoptFile = %v; log:\n%s", out, buf)
	}
	if len(rl.runningFile) == 0 || !strings.Contains(string(rl.runningFile), `"listeners"`) {
		t.Fatalf("runningFile = %s, want the adopted file", rl.runningFile)
	}
}

// A plane-owned process ignores its file, so it never reports it: a report
// would read on the page as the configuration that runs.
func TestAPlaneOwnedHeartbeatReportsNothing(t *testing.T) {
	rl, _ := testReloader(t, reloadBase)
	rl.configPath = "sidecar.yaml"
	rl.load = func(string) (*Config, error) { return LoadConfigBytes([]byte(reloadBase)) }

	reports := heartbeatReports(t, rl, reloadBase)
	if len(reports) == 0 {
		t.Fatal("no heartbeat ran")
	}
	for i, r := range reports {
		if len(r) != 0 {
			t.Fatalf("heartbeat %d reported the ignored file: %s", i, r)
		}
	}
}
