package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// TTFVSurveyResponse is one answer to the in-app TTFV survey. The table is
// append-only: repeated "no" answers each add a row, so the response history
// is preserved rather than only the terminal state.
type TTFVSurveyResponse struct {
	OrgID        string
	UserID       string
	ReachedValue bool
	// Which activity delivered the value. Set only when ReachedValue is true.
	Activity *string
	// Free text detail, set only when Activity is the "other" option.
	ActivityOther *string
}

// TTFVMeasurement is the duration a recorded answer yields, measured entirely
// by the database so the gateway's own clock never enters the metric.
type TTFVMeasurement struct {
	// The t0 of the duration. Nil when orgs.created_at holds NULL: the column
	// carries a default but no NOT NULL constraint, so a row predating it is
	// possible, and such an organization has no measurable TTFV.
	OrgCreatedAt *time.Time `gorm:"column:org_created_at"`
	// Seconds between the organization's creation and this answer. Nil exactly
	// when OrgCreatedAt is.
	TTFVSeconds *int64 `gorm:"column:ttfv_seconds"`
}

// ShouldShowTTFVSurvey reports whether the organization is in a state where the
// TTFV survey may still be asked: it has a creation timestamp to measure from,
// it never confirmed value, and it has not declined within the cooldown.
//
// The window is evaluated by Postgres on purpose, exactly like the signup
// origin survey. orgs.created_at and ttfv_survey_responses.created_at are
// TIMESTAMP WITHOUT TIME ZONE written by NOW(), so comparing them against the
// gateway's clock would drift by the database's UTC offset. Both sides of this
// comparison come from the same clock instead.
//
// The caller-dependent half of the policy — administrator, not anonymous,
// feature flag — is not here; see services.ShouldShowTTFVSurvey.
//
// Propagates gorm.ErrRecordNotFound when no such organization exists.
func ShouldShowTTFVSurvey(db *gorm.DB, orgID string, cooldownDays int) (bool, error) {
	var showSurvey bool
	// Every clause is an IS NOT NULL or a NOT EXISTS, neither of which can
	// evaluate to NULL, so the result always scans into a plain bool.
	res := db.Raw(`
		SELECT o.created_at IS NOT NULL
		   AND NOT EXISTS (
		       SELECT 1 FROM private.ttfv_survey_responses r
		       WHERE r.org_id = o.id AND r.reached_value)
		   AND NOT EXISTS (
		       SELECT 1 FROM private.ttfv_survey_responses r
		       WHERE r.org_id = o.id AND NOT r.reached_value
		         AND r.created_at > NOW() - make_interval(days => ?))
		FROM private.orgs o
		WHERE o.id::TEXT = ?`,
		cooldownDays, orgID,
	).Scan(&showSurvey)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, gorm.ErrRecordNotFound
	}
	return showSurvey, nil
}

// CreateTTFVSurveyResponse records one answer and returns the TTFV duration it
// implies.
//
// The insert is conditional on no confirmed value existing yet, which settles
// every submission that arrives after one was committed. It is not what makes
// the rule safe under concurrency: two "yes" submissions racing each other both
// read an organization that has not confirmed, so both pass. The partial unique
// index on the table is the actual arbiter and turns the loser into a unique
// violation, reported here as the same conflict.
//
// Returns ErrAlreadyExists when the organization already confirmed value, and
// gorm.ErrRecordNotFound when no such organization exists.
func CreateTTFVSurveyResponse(db *gorm.DB, resp *TTFVSurveyResponse) (*TTFVMeasurement, error) {
	var measurement TTFVMeasurement
	// The duration is subtracted inside the database so both endpoints come
	// from one clock. It is NULL exactly when orgs.created_at is.
	res := db.Raw(`
		WITH org AS (
		    SELECT id, created_at FROM private.orgs WHERE id::TEXT = ?
		), inserted AS (
		    INSERT INTO private.ttfv_survey_responses
		        (org_id, user_id, reached_value, activity, activity_other)
		    SELECT org.id, ?, ?, ?, ?
		    FROM org
		    WHERE NOT EXISTS (
		        SELECT 1 FROM private.ttfv_survey_responses r
		        WHERE r.org_id = org.id AND r.reached_value)
		    RETURNING created_at
		)
		SELECT org.created_at AS org_created_at,
		       EXTRACT(EPOCH FROM (inserted.created_at - org.created_at))::BIGINT AS ttfv_seconds
		FROM inserted CROSS JOIN org`,
		resp.OrgID, resp.UserID, resp.ReachedValue, resp.Activity, resp.ActivityOther,
	).Scan(&measurement)
	switch {
	// The only unique constraint on the table is the one confirmed value per
	// organization, so this is unambiguous: a concurrent submission committed
	// first. Translation to this sentinel is enabled globally in database.go.
	case errors.Is(res.Error, gorm.ErrDuplicatedKey):
		return nil, ErrAlreadyExists
	case res.Error != nil:
		return nil, res.Error
	}
	if res.RowsAffected == 1 {
		return &measurement, nil
	}
	// Nothing was inserted: either the organization is unknown or it already
	// confirmed value. Disambiguate so the caller can tell those two apart.
	var found bool
	lookup := db.Raw(`SELECT true FROM private.orgs WHERE id::TEXT = ?`, resp.OrgID).Scan(&found)
	if lookup.Error != nil {
		return nil, lookup.Error
	}
	if lookup.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return nil, ErrAlreadyExists
}
