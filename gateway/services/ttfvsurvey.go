package services

import (
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// ttfvSurveyCooldownDays is how long a "no" suppresses the survey. A "no" is
// not terminal — TTFV is the moment an admin *first* says yes, so declining is
// a data point that has to be recorded and then followed by another ask later.
//
// The window is also the resolution of the metric. Value is reached somewhere
// between the last decline and the yes, but only the yes carries a timestamp,
// so every measurement is inflated by up to one cooldown. Three days keeps that
// error small without putting the question in front of a daily administrator
// more than about twice a week; at one day anyone who does not log in daily
// would meet it on essentially every visit.
const ttfvSurveyCooldownDays = 3

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
// There is no feature flag. The survey is on for every organization and closes
// itself the first time an admin confirms value; the organization's analytics
// mode and whether it has run anything at all are the only things that suppress
// it, and both are checked in SQL alongside the rest of the policy — see
// models.ShouldShowTTFVSurvey.
//
// Propagates gorm.ErrRecordNotFound when the organization does not exist.
func ShouldShowTTFVSurvey(db *gorm.DB, orgID string, isAdmin, isAnonymous bool) (bool, error) {
	if !isAdmin || isAnonymous {
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
