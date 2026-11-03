package contexts

import (
	"errors"
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
