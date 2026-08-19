package services

import (
	"testing"
)

// The whole point of computing the visibility on the server is that the policy
// can be re-tuned without a frontend deploy, which only holds while every
// caller-dependent clause keeps answering false on its own.
//
// The database half of the policy — the analytics mode, the terminal yes and
// the cooldown window — is exercised against real SQL in
// gateway/models/ttfvsurvey_test.go. Here the database handle is deliberately
// nil: any case that reaches it would panic, which is exactly the assertion,
// since a caller who can never be asked must not cost a query on every
// /userinfo.
func TestShouldShowTTFVSurveyShortCircuits(t *testing.T) {
	for _, tc := range []struct {
		name        string
		isAdmin     bool
		isAnonymous bool
	}{
		{name: "never asks a non-admin", isAdmin: false},
		{name: "never asks an anonymous user", isAdmin: true, isAnonymous: true},
		{name: "never asks an anonymous non-admin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ShouldShowTTFVSurvey(nil, "org-"+tc.name, tc.isAdmin, tc.isAnonymous)
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
