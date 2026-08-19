package services

import (
	"testing"

	"github.com/hoophq/hoop/common/featureflag"
)

// The whole point of computing the visibility on the server is that the policy
// can be re-tuned without a frontend deploy, which only holds while every
// caller-dependent clause keeps answering false on its own.
//
// The database half of the policy — the terminal yes and the cooldown window —
// is exercised against real SQL in gateway/models/ttfvsurvey_test.go. Here the
// database handle is deliberately nil: any case that reaches it would panic,
// which is exactly the assertion, since a caller who can never be asked must
// not cost a query on every /userinfo.
func TestShouldShowTTFVSurveyShortCircuits(t *testing.T) {
	for _, tc := range []struct {
		name        string
		isAdmin     bool
		isAnonymous bool
		flagEnabled bool
	}{
		{name: "never asks a non-admin", isAdmin: false, flagEnabled: true},
		{name: "never asks an anonymous user", isAdmin: true, isAnonymous: true, flagEnabled: true},
		{name: "never asks while the flag is off", isAdmin: true, flagEnabled: false},
		{name: "never asks a non-admin while the flag is off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Distinct per case so the process-local flag cache set by one
			// subtest cannot leak into another.
			orgID := "org-" + tc.name
			featureflag.Set(orgID, TTFVSurveyFlagName, tc.flagEnabled)

			got, err := ShouldShowTTFVSurvey(nil, orgID, tc.isAdmin, tc.isAnonymous)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got {
				t.Error("ShouldShowTTFVSurvey = true, want false")
			}
		})
	}
}

// A "no" is not terminal, so the cooldown is the only thing keeping the survey
// from being re-offered on the very next page load.
func TestTTFVSurveyCooldownIsPositive(t *testing.T) {
	if ttfvSurveyCooldownDays <= 0 {
		t.Fatalf("ttfvSurveyCooldownDays = %d, must be greater than zero", ttfvSurveyCooldownDays)
	}
}

// The flag has to exist in the catalog or featureflag.IsEnabled logs a warning
// and returns false forever, which would silently disable the survey for good.
func TestTTFVSurveyFlagIsRegistered(t *testing.T) {
	flag, ok := featureflag.Lookup(TTFVSurveyFlagName)
	if !ok {
		t.Fatalf("flag %q is not registered in the featureflag catalog", TTFVSurveyFlagName)
	}
	if flag.Default {
		t.Error("the survey must be off on a fresh deployment")
	}
}
