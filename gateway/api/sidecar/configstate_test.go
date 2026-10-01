package apisidecar

import (
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
)

func TestConfigState(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	seen := now.Add(-time.Minute)
	fresh := now.Add(-time.Minute)
	stale := now.Add(-10 * time.Minute)
	str := func(s string) *string { return &s }
	on := true

	cases := []struct {
		name string
		row  models.Sidecar
		want string
	}{
		{"never handshaked", models.Sidecar{ServedRevision: str("r1")}, ""},
		{"nothing served", models.Sidecar{LastSeenAt: &seen}, ""},
		{"runs its own file", models.Sidecar{LastSeenAt: &seen, ServedRevision: str("r1"),
			Configuration: models.SidecarConfiguration(daemon.Config{LoadFromDisk: &on})}, ""},
		{"applied", models.Sidecar{LastSeenAt: &seen, ServedRevision: str("r1"), AppliedRevision: str("r1"),
			LastOutcome: str("applied")}, configStateApplied},
		{"unchanged on the served revision", models.Sidecar{LastSeenAt: &seen, ServedRevision: str("r1"),
			AppliedRevision: str("r1"), LastOutcome: str("unchanged")}, configStateApplied},
		{"refused wins over the revisions", models.Sidecar{LastSeenAt: &seen, ServedRevision: str("r2"),
			AppliedRevision: str("r1"), LastOutcome: str("refused")}, configStateRefused},
		{"restart wins over the revisions", models.Sidecar{LastSeenAt: &seen, ServedRevision: str("r2"),
			AppliedRevision: str("r1"), LastOutcome: str("restart")}, configStateRestart},
		{"served a moment ago", models.Sidecar{LastSeenAt: &seen, ServedRevision: str("r2"), ServedRevisionAt: &fresh,
			AppliedRevision: str("r1"), LastOutcome: str("applied")}, configStateApplying},
		{"served long ago and never reported", models.Sidecar{LastSeenAt: &seen, ServedRevision: str("r2"),
			ServedRevisionAt: &stale, AppliedRevision: str("r1"), LastOutcome: str("applied")}, configStateNotApplied},
		{"boot crash-loop on a fresh row", models.Sidecar{LastSeenAt: &seen, ServedRevision: str("r1"),
			ServedRevisionAt: &stale, Capabilities: []string{"review_mode"}}, configStateNotApplied},
		{"too old to report", models.Sidecar{LastSeenAt: &seen, ServedRevision: str("r1"),
			ServedRevisionAt: &stale, Capabilities: []string{}}, configStateUnknown},
		{"refused at the first handshake", models.Sidecar{LastOutcome: str(models.SidecarOutcomeNotServed),
			LastError: str("too old")}, configStateNotServed},
		{"refused at serve after handshakes", models.Sidecar{LastSeenAt: &seen, ServedRevision: str("r1"),
			AppliedRevision: str("r1"), LastOutcome: str(models.SidecarOutcomeNotServed)}, configStateNotServed},
	}
	for _, tc := range cases {
		if got := configState(tc.row, now); got != tc.want {
			t.Errorf("%s: config_state = %q, want %q", tc.name, got, tc.want)
		}
	}
}
