package contexts

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hoophq/hoop/common/featureflag"
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
		{name: "older gateway without the flag", flags: map[string]bool{"beta.oracle_native": true}},
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
