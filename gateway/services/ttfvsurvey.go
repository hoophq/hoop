package services

import (
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// TTFVSurveyFlagName gates the whole survey. Because the frontend derives its
// entire visibility from show_ttfv_survey, flipping this off is a real kill
// switch: no widget renders anywhere, with no frontend deploy.
const TTFVSurveyFlagName = "experimental.ttfv_survey"

// ttfvSurveyCooldownDays is how long a "no" suppresses the survey. A "no" is
// not terminal — TTFV is the moment an admin *first* says yes, so declining is
// a data point that has to be recorded and then followed by another ask later.
const ttfvSurveyCooldownDays = 7

// TTFVSurveyAnswer is one validated survey submission. Activity is set only
// when ReachedValue is true, and ActivityOther only alongside the "other"
// activity; enforcing that is the caller's job.
type TTFVSurveyAnswer struct {
	ReachedValue  bool
	Activity      *string
	ActivityOther *string
}

// ShouldShowTTFVSurvey computes the full ask policy for the caller.
//
// The caller-dependent clauses are checked first and short-circuit, so the
// database is only consulted for a caller who could actually be asked.
// Anonymous users, API keys and service accounts are excluded: they have no
// row in private.users and are not the administrator whose confirmation the
// metric is defined by.
//
// Propagates gorm.ErrRecordNotFound when the organization does not exist.
func ShouldShowTTFVSurvey(db *gorm.DB, orgID string, isAdmin, isAnonymous bool) (bool, error) {
	if !isAdmin || isAnonymous || !featureflag.IsEnabled(orgID, TTFVSurveyFlagName) {
		return false, nil
	}
	return models.ShouldShowTTFVSurvey(db, orgID, ttfvSurveyCooldownDays)
}

// RecordTTFVSurveyAnswer stores one answer and returns the TTFV duration it
// implies, for the analytics event.
//
// Only a confirmed value is terminal, so a decline inside the cooldown is still
// recorded — it suppresses the next ask without closing the survey. Authorizing
// the caller is the caller's responsibility.
//
// Returns models.ErrAlreadyExists when the organization already confirmed
// value, and propagates gorm.ErrRecordNotFound when it does not exist.
func RecordTTFVSurveyAnswer(db *gorm.DB, orgID, userID string, answer TTFVSurveyAnswer) (*models.TTFVMeasurement, error) {
	return models.CreateTTFVSurveyResponse(db, &models.TTFVSurveyResponse{
		OrgID:         orgID,
		UserID:        userID,
		ReachedValue:  answer.ReachedValue,
		Activity:      answer.Activity,
		ActivityOther: answer.ActivityOther,
	})
}
