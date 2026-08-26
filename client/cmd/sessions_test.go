package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
)

func TestFormatSessionTotal(t *testing.T) {
	total := func(v int64) *int64 { return &v }

	for _, tt := range []struct {
		name string
		resp sessionsResponse
		want string
	}{
		{
			name: "exact count renders the plain number",
			resp: sessionsResponse{Total: total(1500000)},
			want: "1500000",
		},
		{
			name: "capped count renders a lower bound",
			resp: sessionsResponse{Total: total(10000), TotalIsCapped: true},
			want: "10000+",
		},
		{
			// A capped request whose result set fits under the cap reports the
			// real number, so it must not be suffixed with "+".
			name: "capped count under the cap is exact",
			resp: sessionsResponse{Total: total(1637)},
			want: "1637",
		},
		{
			// --count none: the empty string tells displaySessions to leave the
			// total out of the hint rather than print a misleading zero.
			name: "absent count renders nothing",
			resp: sessionsResponse{Total: nil},
			want: "",
		},
		{
			name: "zero is a real total, not an absent one",
			resp: sessionsResponse{Total: total(0)},
			want: "0",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatSessionTotal(&tt.resp); got != tt.want {
				t.Errorf("formatSessionTotal() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The capped default is the whole point of the change: an exact count scans
// every matching row and this command is the one that lists them all.
func TestSessionsListCountFlagDefaultsToCapped(t *testing.T) {
	flag := sessionsListCmd.Flags().Lookup("count")
	if flag == nil {
		t.Fatal("expected a --count flag on `sessions list`")
	}
	if want := string(models.SessionCountCapped); flag.DefValue != want {
		t.Errorf("--count default = %q, want %q", flag.DefValue, want)
	}
}

// The gateway sends total=null for ?count=none. Decoding that into a plain int
// would yield a genuine-looking zero, which is why Total is a pointer.
func TestSessionsResponseDecodesNullTotal(t *testing.T) {
	for _, tt := range []struct {
		name       string
		body       string
		wantNil    bool
		wantTotal  int64
		wantCapped bool
	}{
		{
			name:      "count=exact",
			body:      `{"data":[],"has_next_page":true,"total":1500000,"total_is_capped":false}`,
			wantTotal: 1500000,
		},
		{
			name:       "count=capped",
			body:       `{"data":[],"has_next_page":true,"total":10000,"total_is_capped":true}`,
			wantTotal:  10000,
			wantCapped: true,
		},
		{
			name:    "count=none",
			body:    `{"data":[],"has_next_page":true,"total":null,"total_is_capped":false}`,
			wantNil: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var resp sessionsResponse
			if err := json.NewDecoder(strings.NewReader(tt.body)).Decode(&resp); err != nil {
				t.Fatalf("failed decoding response: %v", err)
			}
			if tt.wantNil {
				if resp.Total != nil {
					t.Fatalf("expected a nil total, got %d", *resp.Total)
				}
				return
			}
			if resp.Total == nil {
				t.Fatalf("expected total %d, got nil", tt.wantTotal)
			}
			if *resp.Total != tt.wantTotal {
				t.Errorf("total = %d, want %d", *resp.Total, tt.wantTotal)
			}
			if resp.TotalIsCapped != tt.wantCapped {
				t.Errorf("total_is_capped = %v, want %v", resp.TotalIsCapped, tt.wantCapped)
			}
		})
	}
}
