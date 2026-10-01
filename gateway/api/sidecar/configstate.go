package apisidecar

import (
	"time"

	"github.com/hoophq/hoop/gateway/models"
)

// The states a sidecar's configuration can be in, as the page renders them.
// Computed here, from the row and the clock, so the fleet table and the
// sidecar page agree and the rule has one test.
const (
	configStateApplied    = "applied"
	configStateApplying   = "applying"
	configStateNotApplied = "not_applied"
	configStateRefused    = "refused"
	configStateNotServed  = "not_served"
	configStateRestart    = "restart"
	configStateUnknown    = "unknown"
)

// applyGrace is how long a served revision may go unreported before the
// sidecar is read as not having applied it: one heartbeat to take the
// document, one to report it, and slack for a slow tick.
const applyGrace = 150 * time.Second

// configState says what the sidecar runs relative to what it was served.
// Empty when there is nothing to say: no handshake yet, nothing served, or
// a sidecar running its own file, whose document the plane does not own.
//
// A refusal wins over everything, including the guards above: the plane
// refuses a first handshake before it serves anything or marks the sidecar
// seen, and the page would otherwise read Waiting with the reason hidden. A
// restart outcome wins over the revisions. The row holds either until an
// apply replaces it (models.RecordSidecarHandshake). Equal revisions mean
// applied. Unequal ones mean the sidecar has not reported the served
// document, which is normal for a heartbeat after a save and the shape of a
// boot crash-loop after that; a build that reports no outcome and sends no
// capabilities header is too old to say either way.
func configState(s models.Sidecar, now time.Time) string {
	if usesConfigFile(s.Configuration) {
		return ""
	}
	switch derefOrEmpty(s.LastOutcome) {
	case "refused":
		return configStateRefused
	case models.SidecarOutcomeNotServed:
		return configStateNotServed
	}
	if s.LastSeenAt == nil {
		return ""
	}
	served := derefOrEmpty(s.ServedRevision)
	if served == "" {
		return ""
	}
	if derefOrEmpty(s.LastOutcome) == "restart" {
		return configStateRestart
	}
	if derefOrEmpty(s.AppliedRevision) == served {
		return configStateApplied
	}
	if s.LastOutcome == nil && len(s.Capabilities) == 0 {
		return configStateUnknown
	}
	if s.ServedRevisionAt != nil && now.Sub(*s.ServedRevisionAt) > applyGrace {
		return configStateNotApplied
	}
	return configStateApplying
}
