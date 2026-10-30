package sessionapi

import (
	"testing"
	"time"
)

func TestWithinTimeWindow(t *testing.T) {
	at := func(clock string) time.Time {
		if len(clock) == len("15:04") {
			clock += ":00"
		}
		v, err := time.Parse("15:04:05", clock)
		if err != nil {
			t.Fatal(err)
		}
		return time.Date(2026, 10, 30, v.Hour(), v.Minute(), v.Second(), 0, time.UTC)
	}
	for _, tc := range []struct {
		start, end, now string
		want            bool
	}{
		{"09:00", "17:00", "08:59", false},
		{"09:00", "17:00", "09:00", true},
		{"09:00", "17:00", "17:00", true},
		{"09:00", "17:00", "17:01", false},
		// A window that crosses midnight: both sides of midnight are inside.
		{"22:00", "02:00", "21:59", false},
		{"22:00", "02:00", "22:00", true},
		{"22:00", "02:00", "23:30", true},
		{"22:00", "02:00", "00:00", true},
		{"22:00", "02:00", "01:30", true},
		{"22:00", "02:00", "02:00", true},
		// The window ends at the start of its last minute; the web app agrees
		// (webapp_v2/test/sessions/timeWindow.test.js).
		{"22:00", "02:00", "02:00:30", false},
		{"22:00", "02:00", "02:01", false},
		{"22:00", "02:00", "12:00", false},
	} {
		got, err := withinTimeWindow(tc.start, tc.end, at(tc.now))
		if err != nil {
			t.Fatalf("%s-%s at %s: %v", tc.start, tc.end, tc.now, err)
		}
		if got != tc.want {
			t.Errorf("%s-%s at %s: got %v, want %v", tc.start, tc.end, tc.now, got, tc.want)
		}
	}
	if _, err := withinTimeWindow("9h", "17:00", at("10:00")); err == nil {
		t.Error("a malformed start time must fail")
	}
}
