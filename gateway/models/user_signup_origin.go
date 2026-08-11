package models

import (
	"gorm.io/gorm"
)

// originSurveyWindowDays bounds for how long the onboarding "How did you hear
// about Hoop?" survey keeps being offered, counted from the moment the user
// record was created.
const originSurveyWindowDays = 7

// The signup_origin columns are deliberately absent from the User struct.
// UpdateUser persists through DB.Save, which writes every mapped column, and
// gateway/api/signup builds a partial User literal before calling it — mapping
// these columns would let such a call silently reset a stored answer. Keeping
// them out of the struct makes that impossible, and the two functions below are
// the only read/write path.

// ShouldShowOriginSurvey reports whether the user still has to answer the
// signup origin survey: no answer recorded yet and still inside the
// originSurveyWindowDays window.
//
// The window is evaluated by Postgres on purpose. users.created_at is a
// TIMESTAMP WITHOUT TIME ZONE written by NOW(), so comparing it against the
// gateway's own clock would drift by the database's UTC offset. Both sides of
// this comparison come from the same clock instead.
//
// Propagates gorm.ErrRecordNotFound when no such user exists.
func ShouldShowOriginSurvey(db *gorm.DB, orgID, subject string) (bool, error) {
	var showSurvey bool
	res := db.Raw(`
		SELECT signup_origin IS NULL AND created_at > NOW() - make_interval(days => ?)
		FROM private.users
		WHERE org_id = ? AND subject = ?`,
		originSurveyWindowDays, orgID, subject,
	).Scan(&showSurvey)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, gorm.ErrRecordNotFound
	}
	return showSurvey, nil
}

// SetUserSignupOrigin records the survey answer for a user. otherText carries
// the free text of the 'other' option and must be nil for every other answer.
//
// The update is conditional on signup_origin still being NULL so the first
// answer wins even when two tabs submit concurrently — the survey allows a
// single answer per user.
//
// Returns ErrAlreadyExists when an answer was already recorded and
// gorm.ErrRecordNotFound when no such user exists.
func SetUserSignupOrigin(db *gorm.DB, orgID, subject, origin string, otherText *string) error {
	res := db.Exec(`
		UPDATE private.users
		SET signup_origin = ?, signup_origin_other = ?, updated_at = NOW()
		WHERE org_id = ? AND subject = ? AND signup_origin IS NULL`,
		origin, otherText, orgID, subject)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 1 {
		return nil
	}
	// No row matched: either the user is unknown or the answer was already
	// recorded. Disambiguate so the caller can tell those two apart.
	var found bool
	lookup := db.Raw(`SELECT true FROM private.users WHERE org_id = ? AND subject = ?`, orgID, subject).Scan(&found)
	if lookup.Error != nil {
		return lookup.Error
	}
	if lookup.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return ErrAlreadyExists
}
