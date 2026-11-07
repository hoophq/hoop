package services

import "testing"

// The interval steps up once per thousand sidecars and stops at ten minutes:
// a fleet of any size costs the plane about the rate a thousand do.
func TestHeartbeatSecondsFor(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want int
	}{
		{0, 60}, {1, 60}, {1000, 60},
		{1001, 120}, {2000, 120},
		{5111, 360},
		{10000, 600}, {10001, 600}, {1_000_000, 600},
	} {
		if got := heartbeatSecondsFor(tc.n); got != tc.want {
			t.Errorf("heartbeatSecondsFor(%d) = %d, want %d", tc.n, got, tc.want)
		}
	}
}
