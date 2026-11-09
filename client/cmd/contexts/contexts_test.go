package contexts

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

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
		{name: "gateway error", err: errors.New("401"), wantHint: true},
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

func TestHasContextCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the fake binary")
	}
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// what released versions before `hoop context` answer
	old := write("old", `echo 'Error: unknown command "context" for "hoop"' >&2; exit 1`)
	if err := hasContextCommand(old); err == nil {
		t.Fatal("a binary without `context` was accepted")
	}
	newer := write("new", `[ "$1 $2" = "context --help" ] || exit 2; echo usage`)
	if err := hasContextCommand(newer); err != nil {
		t.Fatalf("a binary with `context` was refused: %v", err)
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

	apiURLFlag, apiKeyFlag = "http://a:1", "hpk_old"
	if err := run(createCmd, "dev"); err != nil {
		t.Fatal(err)
	}
	apiURLFlag, apiKeyFlag, skipSyncFlag = "", "", true
	if err := run(useCmd, "dev"); err != nil {
		t.Fatal(err)
	}
	// a new key for the active context would be undone by the next save-back
	apiURLFlag, apiKeyFlag, overwriteFlag = "http://a:1", "hpk_new", true
	if err := run(createCmd, "dev"); err == nil {
		t.Fatal("replaced the active context")
	}
	// copying config.toml over it stays allowed: that is its current state
	apiURLFlag, apiKeyFlag = "", ""
	if err := run(createCmd, "dev"); err != nil {
		t.Fatalf("copy over the active context: %v", err)
	}
}
