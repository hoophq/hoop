package contexts

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/spf13/cobra"
)

func TestDecideFlag(t *testing.T) {
	for _, tc := range []struct {
		name     string
		flags    map[string]bool
		err      error
		wantErr  bool
		wantHint bool
	}{
		{name: "flag on", flags: map[string]bool{featureflag.FlagCLIContexts: true}},
		{name: "older gateway without the flag", flags: map[string]bool{"beta.oracle_native": true}, wantErr: true},
		{name: "no flags at all", flags: nil, wantErr: true},
		{name: "flag off", flags: map[string]bool{featureflag.FlagCLIContexts: false}, wantErr: true},
		{name: "no login", err: errNoLogin, wantHint: true},
		{name: "expired login", err: &clientconfig.StatusError{Code: http.StatusUnauthorized}, wantHint: true},
		{name: "login refused", err: &clientconfig.StatusError{Code: http.StatusForbidden}, wantHint: true},
		{name: "gateway error", err: &clientconfig.StatusError{Code: http.StatusBadGateway}, wantErr: true},
		{name: "gateway unreachable", err: errors.New("dial tcp: connection refused"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := decideFlag("prd", tc.flags, tc.err)
			if (got.err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", got.err, tc.wantErr)
			}
			if (got.syncHint != "") != tc.wantHint {
				t.Fatalf("syncHint = %q, wantHint %v", got.syncHint, tc.wantHint)
			}
		})
	}
}

// An org gets `hoop context use` only after an admin turns the flag on.
func TestCLIContextsFlagIsOffByDefault(t *testing.T) {
	f, ok := featureflag.Lookup(featureflag.FlagCLIContexts)
	if !ok {
		t.Fatalf("%s is not in the catalog", featureflag.FlagCLIContexts)
	}
	if f.Default {
		t.Fatalf("%s defaults to on, want off", featureflag.FlagCLIContexts)
	}
	if got := featureflag.SnapshotForOrg("org-without-overrides")[featureflag.FlagCLIContexts]; got {
		t.Fatal("serverinfo reports the flag on for an org that never set it")
	}
	if gate := decideFlag("prd", featureflag.SnapshotForOrg("org-without-overrides"), nil); gate.err == nil {
		t.Fatal("use is allowed on a gateway at the default")
	}
}

func TestBuildContextFlags(t *testing.T) {
	home := t.TempDir()
	s := clientconfig.NewContextStore(home)
	set := func(api, grpcURL, key, ca string) {
		apiURLFlag, grpcURLFlag, apiKeyFlag, tlsCAFlag = api, grpcURL, key, ca
	}
	t.Cleanup(func() { set("", "", "", "") })

	set("https://dev.example.com/", "grpcs://dev.example.com:8443", "hpk_x", "")
	ctx, err := buildContext(s, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if ctx.ApiURL != "https://dev.example.com" || ctx.GrpcURL != "grpcs://dev.example.com:8443" || ctx.Token != "hpk_x" {
		t.Fatalf("context = %+v", ctx)
	}

	caFile := filepath.Join(home, "ca.pem")
	if err := os.WriteFile(caFile, []byte("CA"), 0600); err != nil {
		t.Fatal(err)
	}
	set("http://127.0.0.1:8009", "", "", caFile)
	if ctx, err = buildContext(s, "dev"); err != nil || ctx.TlsCAB64Enc != base64.StdEncoding.EncodeToString([]byte("CA")) {
		t.Fatalf("tls ca: %+v, %v", ctx, err)
	}

	for _, tc := range []struct{ name, api, grpcURL, key, ca string }{
		{"grpc scheme as api url", "grpc://gw:9000", "", "", ""},
		{"no scheme", "gw:8009", "", "", ""},
		{"api key without prefix", "https://gw", "", "xapi-1", ""},
		{"bad grpc url", "https://gw", "::bad", "", ""},
		{"grpc url without api url", "", "grpc://gw:8010", "", ""},
		{"missing ca file", "https://gw", "", "", filepath.Join(home, "missing.pem")},
		{"copy with no current config", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set(tc.api, tc.grpcURL, tc.key, tc.ca)
			if ctx, err := buildContext(s, "dev"); err == nil {
				t.Fatalf("accepted: %+v", ctx)
			}
		})
	}
	set("https://gw", "", "", "")
	if _, err := buildContext(s, "../x"); err == nil {
		t.Fatal("accepted an invalid name")
	}
}

func TestCreateRefusesToReplaceActiveContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Cleanup(func() { apiURLFlag, apiKeyFlag, overwriteFlag, skipSyncFlag = "", "", false, false })
	run := func(cmd *cobra.Command, args ...string) error { return cmd.RunE(cmd, args) }
	gw := fakeGateway(t, true)

	apiURLFlag, apiKeyFlag = gw, "hpk_old"
	if err := run(createCmd, "dev"); err != nil {
		t.Fatal(err)
	}
	apiURLFlag, apiKeyFlag, skipSyncFlag = "", "", true
	if err := run(useCmd, "dev"); err != nil {
		t.Fatal(err)
	}
	// a new key for the active context would be undone by the next save-back
	apiURLFlag, apiKeyFlag, overwriteFlag = gw, "hpk_new", true
	if err := run(createCmd, "dev"); err == nil {
		t.Fatal("replaced the active context")
	}
	// copying config.toml over it stays allowed: that is its current state
	apiURLFlag, apiKeyFlag = "", ""
	if err := run(createCmd, "dev"); err != nil {
		t.Fatalf("copy over the active context: %v", err)
	}
}

func TestCreateCopyKeepsLoginOfActive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Cleanup(func() { apiURLFlag, apiKeyFlag, overwriteFlag, skipSyncFlag = "", "", false, false })
	run := func(cmd *cobra.Command, args ...string) error { return cmd.RunE(cmd, args) }
	gw := fakeGateway(t, true)

	apiURLFlag, apiKeyFlag = gw, "hpk_old"
	if err := run(createCmd, "dev"); err != nil {
		t.Fatal(err)
	}
	apiURLFlag, apiKeyFlag, skipSyncFlag = "", "", true
	if err := run(useCmd, "dev"); err != nil {
		t.Fatal(err)
	}
	// `hoop login` refreshes the token in config.toml only
	if err := os.WriteFile(filepath.Join(home, ".hoop", "config.toml"),
		[]byte("api_url = \""+gw+"\"\ntoken = \"hpk_new\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(createCmd, "dev-copy"); err != nil {
		t.Fatal(err)
	}
	s := clientconfig.NewContextStore(filepath.Join(home, ".hoop"))
	dev, err := s.Get("dev")
	if err != nil {
		t.Fatal(err)
	}
	if dev.Token != "hpk_new" {
		t.Fatalf("dev token = %q, want the login made before the copy", dev.Token)
	}
	if active, _ := s.Active(); active != "dev-copy" {
		t.Fatalf("active = %q, want dev-copy", active)
	}
}

// fakeGateway answers /api/serverinfo with experimental.cli_contexts set to on.
func fakeGateway(t *testing.T, on bool) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/serverinfo" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"feature_flags": map[string]bool{featureflag.FlagCLIContexts: on}})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestUseRefusedWhenFlagOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Cleanup(func() { apiURLFlag, apiKeyFlag = "", "" })
	run := func(cmd *cobra.Command, args ...string) error { return cmd.RunE(cmd, args) }

	apiURLFlag, apiKeyFlag = fakeGateway(t, false), "hpk_x"
	if err := run(createCmd, "prd"); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, ".hoop", "config.toml")
	before := []byte("api_url = \"https://dev.example.com\"\n")
	if err := os.WriteFile(cfg, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(useCmd, "prd"); err == nil {
		t.Fatal("use was allowed with the flag off")
	}
	if after, _ := os.ReadFile(cfg); string(after) != string(before) {
		t.Fatalf("config.toml changed after a refused use: %q", after)
	}
}

func TestRemoveRefusesActiveAndShowOmitsToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Cleanup(func() { apiURLFlag, apiKeyFlag, skipSyncFlag, forceFlag = "", "", false, false })
	run := func(cmd *cobra.Command, args ...string) error { return cmd.RunE(cmd, args) }

	apiURLFlag, apiKeyFlag = fakeGateway(t, true), "hpk_secret"
	if err := run(createCmd, "dev"); err != nil {
		t.Fatal(err)
	}
	apiURLFlag, apiKeyFlag, skipSyncFlag = "", "", true
	if err := run(useCmd, "dev"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := run(showCmd); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "hpk_secret") || !strings.Contains(out, "token=OMITTED") {
		t.Fatalf("show printed:\n%s", out)
	}
	if err := run(removeCmd, "dev"); err == nil {
		t.Fatal("removed the active context without --force")
	}
	forceFlag = true
	if err := run(removeCmd, "dev"); err != nil {
		t.Fatalf("remove --force: %v", err)
	}
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = stdout }()
	f()
	_ = w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

// create, use and remove wait while another hoop process holds the store lock.
func TestCommandsWaitForTheStoreLock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Cleanup(func() { apiURLFlag, apiKeyFlag, skipSyncFlag, forceFlag = "", "", false, false })
	gw := fakeGateway(t, true)
	apiURLFlag, apiKeyFlag, skipSyncFlag, forceFlag = gw, "hpk_x", true, true

	for _, step := range []struct {
		cmd  *cobra.Command
		args []string
	}{{createCmd, []string{"dev"}}, {useCmd, []string{"dev"}}, {removeCmd, []string{"dev"}}} {
		t.Run(step.cmd.Name(), func(t *testing.T) {
			// another process: its own store and lock file handle
			unlock, err := clientconfig.NewContextStore(filepath.Join(home, ".hoop")).Lock()
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- step.cmd.RunE(step.cmd, step.args) }()
			select {
			case err := <-done:
				unlock()
				t.Fatalf("%s ran while the lock was held (err=%v)", step.cmd.Name(), err)
			case <-time.After(300 * time.Millisecond):
			}
			unlock()
			if err := <-done; err != nil {
				t.Fatalf("%s after unlock: %v", step.cmd.Name(), err)
			}
		})
	}
}
