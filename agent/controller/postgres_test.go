package controller

import (
	"encoding/json"
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
)

func TestSetPostgresFastMasking(t *testing.T) {
	for _, tt := range []struct {
		name  string
		flags map[string]bool
		want  string
	}{
		{name: "flag on", flags: map[string]bool{postgresFastMaskingFlag: true}, want: "true"},
		{name: "flag off", flags: map[string]bool{postgresFastMaskingFlag: false}},
		{name: "no snapshot from the gateway", flags: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{}
			if tt.flags != nil {
				snapshot, _ := json.Marshal(tt.flags)
				a.flags.Update(map[string][]byte{pb.SpecFeatureFlagsKey: snapshot})
			}
			opts := map[string]string{}
			a.setPostgresFastMasking(opts)
			got, ok := opts["postgres_fast_masking"]
			if got != tt.want || ok != (tt.want != "") {
				t.Fatalf("opts[postgres_fast_masking] = %q (set=%v), want %q", got, ok, tt.want)
			}
		})
	}
}
