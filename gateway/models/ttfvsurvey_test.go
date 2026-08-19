package models_test

import (
	"errors"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// ttfvCooldownDays mirrors the window services.ShouldShowTTFVSurvey passes in.
// It is restated rather than imported so this file exercises the SQL at the
// real width without the models package depending on the services package.
const ttfvCooldownDays = 7

// seedTTFVOrg creates an organization whose created_at is createdDaysAgo days
// in the past, so a TTFV duration can be asserted without waiting.
func seedTTFVOrg(t *testing.T, orgID, name string, createdDaysAgo float64) {
	t.Helper()
	err := models.DB.Exec(`
		INSERT INTO private.orgs (id, name, created_at)
		VALUES (?, ?, NOW() - make_interval(secs => ?))`,
		orgID, name, createdDaysAgo*24*60*60,
	).Error
	if err != nil {
		t.Fatalf("seed org %s: %v", name, err)
	}
}

// seedTTFVActiveOrg creates an organization that has run at least one session,
// which is what an ordinary organization eligible for the survey looks like.
// Use seedTTFVOrg directly only to build one that has never used the product.
func seedTTFVActiveOrg(t *testing.T, orgID, name string, createdDaysAgo float64) {
	t.Helper()
	seedTTFVOrg(t, orgID, name, createdDaysAgo)
	// Only the existence of a session matters to the query, so the row carries
	// the minimum the schema requires.
	err := models.DB.Exec(`
		INSERT INTO private.sessions (org_id, connection, connection_type, verb, status)
		VALUES (?, 'pgdemo', 'postgres', 'exec', 'done')`, orgID).Error
	if err != nil {
		t.Fatalf("seed session for org %s: %v", orgID, err)
	}
}

// setTTFVAnalyticsMode moves an organization off the 'identified' default.
// Applied after seeding rather than as a seedTTFVOrg argument so that every
// other case keeps exercising the default an ordinary organization really has.
func setTTFVAnalyticsMode(t *testing.T, orgID, mode string) {
	t.Helper()
	err := models.DB.Exec(
		`UPDATE private.orgs SET analytics_mode = ? WHERE id = ?`, mode, orgID).Error
	if err != nil {
		t.Fatalf("set analytics_mode=%s on org %s: %v", mode, orgID, err)
	}
}

// seedTTFVResponse writes one answer createdDaysAgo days in the past.
func seedTTFVResponse(t *testing.T, orgID string, reachedValue bool, createdDaysAgo float64) {
	t.Helper()
	err := models.DB.Exec(`
		INSERT INTO private.ttfv_survey_responses (org_id, user_id, reached_value, created_at)
		VALUES (?, 'seed-user', ?, NOW() - make_interval(secs => ?))`,
		orgID, reachedValue, createdDaysAgo*24*60*60,
	).Error
	if err != nil {
		t.Fatalf("seed response for org %s: %v", orgID, err)
	}
}

func TestShouldShowTTFVSurvey(t *testing.T) {
	// Reuses the embedded-database helper from user_signup_origin_test.go:
	// same package, and the predicate below is raw SQL against the real schema,
	// where an in-memory fake would exercise none of what can break — the
	// NOT EXISTS subqueries and the interval arithmetic.
	startTestDB(t)

	const (
		orgNeverAnswered      = "00000000-0000-0000-0000-0000000000b1"
		orgDeclinedNow        = "00000000-0000-0000-0000-0000000000b2"
		orgDeclinedOld        = "00000000-0000-0000-0000-0000000000b3"
		orgAnsweredYes        = "00000000-0000-0000-0000-0000000000b4"
		orgNoCreatedAt        = "00000000-0000-0000-0000-0000000000b5"
		orgAnalyticsDisabled  = "00000000-0000-0000-0000-0000000000b6"
		orgAnalyticsAnonymous = "00000000-0000-0000-0000-0000000000b7"
		orgNoSessions         = "00000000-0000-0000-0000-0000000000b8"
	)

	// Seeded without an analytics_mode, so it takes the 'identified' column
	// default. That is the point: there is no feature flag any more, so an
	// ordinary organization with nothing configured must be asked.
	seedTTFVActiveOrg(t, orgNeverAnswered, "ttfv-never-answered", 30)

	// Signed up but never ran anything, so "did you get done what you came here
	// to do?" has nothing to refer to. Old enough that no age threshold would
	// have caught it — the precondition is activity, not the calendar.
	seedTTFVOrg(t, orgNoSessions, "ttfv-no-sessions", 30)

	// An organization that turned analytics off already asked not to be
	// measured, and analytics.Track would drop the event, so the question is not
	// put on screen at all.
	seedTTFVActiveOrg(t, orgAnalyticsDisabled, "ttfv-analytics-disabled", 30)
	setTTFVAnalyticsMode(t, orgAnalyticsDisabled, "disabled")

	// 'anonymous' still collects, it only strips the identity, so it is asked.
	// Only 'disabled' suppresses the survey.
	seedTTFVActiveOrg(t, orgAnalyticsAnonymous, "ttfv-analytics-anonymous", 30)
	setTTFVAnalyticsMode(t, orgAnalyticsAnonymous, "anonymous")

	// Bracket the cooldown boundary from both sides without landing on it, so
	// the assertions cannot flake on clock resolution.
	seedTTFVActiveOrg(t, orgDeclinedNow, "ttfv-declined-now", 30)
	seedTTFVResponse(t, orgDeclinedNow, false, 6.95)

	seedTTFVActiveOrg(t, orgDeclinedOld, "ttfv-declined-old", 30)
	seedTTFVResponse(t, orgDeclinedOld, false, 7.05)

	// Two declines then a yes: the yes is terminal regardless of the older rows,
	// including the one that has already aged out of the cooldown.
	seedTTFVActiveOrg(t, orgAnsweredYes, "ttfv-answered-yes", 30)
	seedTTFVResponse(t, orgAnsweredYes, false, 20)
	seedTTFVResponse(t, orgAnsweredYes, false, 10)
	seedTTFVResponse(t, orgAnsweredYes, true, 1)

	// orgs.created_at carries a default but no NOT NULL constraint, so a row
	// can hold NULL there. Such an organization has no duration to measure, so
	// asking it would produce an answer that cannot become a data point.
	if err := models.DB.Exec(
		`INSERT INTO private.orgs (id, name, created_at) VALUES (?, 'ttfv-no-created-at', NULL)`,
		orgNoCreatedAt).Error; err != nil {
		t.Fatalf("seed org without created_at: %v", err)
	}
	// Given a session so the missing timestamp is the only reason it is not
	// asked; otherwise the case would pass even if that clause were dropped.
	if err := models.DB.Exec(`
		INSERT INTO private.sessions (org_id, connection, connection_type, verb, status)
		VALUES (?, 'pgdemo', 'postgres', 'exec', 'done')`, orgNoCreatedAt).Error; err != nil {
		t.Fatalf("seed session for org without created_at: %v", err)
	}

	tests := []struct {
		name  string
		orgID string
		want  bool
	}{
		{name: "never answered, nothing configured", orgID: orgNeverAnswered, want: true},
		{name: "declined inside the cooldown", orgID: orgDeclinedNow, want: false},
		{name: "declined outside the cooldown", orgID: orgDeclinedOld, want: true},
		{name: "a yes is terminal", orgID: orgAnsweredYes, want: false},
		{name: "organization without a creation timestamp", orgID: orgNoCreatedAt, want: false},
		{name: "analytics disabled", orgID: orgAnalyticsDisabled, want: false},
		{name: "analytics anonymous", orgID: orgAnalyticsAnonymous, want: true},
		{name: "never ran a session", orgID: orgNoSessions, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := models.ShouldShowTTFVSurvey(models.DB, tt.orgID, ttfvCooldownDays)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ShouldShowTTFVSurvey = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("unknown organization propagates not found", func(t *testing.T) {
		_, err := models.ShouldShowTTFVSurvey(models.DB, "00000000-0000-0000-0000-0000000000ff", ttfvCooldownDays)
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Errorf("expected gorm.ErrRecordNotFound, got %v", err)
		}
	})
}

func TestCreateTTFVSurveyResponse(t *testing.T) {
	startTestDB(t)

	t.Run("measures the duration since the organization was created", func(t *testing.T) {
		const orgID = "00000000-0000-0000-0000-0000000000c1"
		// 10 days old, so the returned duration has a value worth asserting.
		// Active, so that the assertion below — that the stored yes is what
		// closes the survey — cannot pass merely because it was never eligible.
		seedTTFVActiveOrg(t, orgID, "ttfv-create", 10)

		activity := "connected-infra-resource"
		detail := "Configured SSO"
		measurement, err := models.CreateTTFVSurveyResponse(models.DB, &models.TTFVSurveyResponse{
			OrgID:         orgID,
			UserID:        "admin@hoop.dev",
			ReachedValue:  true,
			Activity:      &activity,
			ActivityOther: &detail,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if measurement.OrgCreatedAt == nil || measurement.TTFVSeconds == nil {
			t.Fatalf("expected a measurable duration, got %+v", measurement)
		}
		// Both endpoints come from the database clock, so the gap is the seeded
		// 10 days give or take the time the test itself took.
		const tenDays = 10 * 24 * 60 * 60
		if *measurement.TTFVSeconds < tenDays || *measurement.TTFVSeconds > tenDays+60 {
			t.Errorf("ttfv_seconds = %d, want ~%d", *measurement.TTFVSeconds, tenDays)
		}

		showSurvey, err := models.ShouldShowTTFVSurvey(models.DB, orgID, ttfvCooldownDays)
		if err != nil {
			t.Fatalf("read state: %v", err)
		}
		if showSurvey {
			t.Error("expected the stored yes to close the survey")
		}
	})

	t.Run("an organization without a creation time yields no duration", func(t *testing.T) {
		const orgID = "00000000-0000-0000-0000-0000000000c4"
		if err := models.DB.Exec(
			`INSERT INTO private.orgs (id, name, created_at) VALUES (?, 'ttfv-create-no-created-at', NULL)`,
			orgID).Error; err != nil {
			t.Fatalf("seed org without created_at: %v", err)
		}

		measurement, err := models.CreateTTFVSurveyResponse(models.DB, &models.TTFVSurveyResponse{
			OrgID: orgID, UserID: "admin@hoop.dev",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// The answer is still recorded; only the duration is unknown, and a
		// NULL creation time must not be reported as a TTFV of zero.
		if measurement.OrgCreatedAt != nil || measurement.TTFVSeconds != nil {
			t.Errorf("expected no duration, got %+v", measurement)
		}
	})

	t.Run("a decline stores no activity", func(t *testing.T) {
		const orgID = "00000000-0000-0000-0000-0000000000c2"
		seedTTFVOrg(t, orgID, "ttfv-decline", 3)

		if _, err := models.CreateTTFVSurveyResponse(models.DB, &models.TTFVSurveyResponse{
			OrgID: orgID, UserID: "admin@hoop.dev",
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var bothNull bool
		err := models.DB.Raw(`
			SELECT activity IS NULL AND activity_other IS NULL
			FROM private.ttfv_survey_responses WHERE org_id = ?`, orgID).Scan(&bothNull).Error
		if err != nil {
			t.Fatalf("read stored row: %v", err)
		}
		if !bothNull {
			t.Error("expected activity and activity_other to stay null for a decline")
		}
	})

	// The table is append-only on purpose: repeated declines are the response
	// history the metric is reconstructed from, not state to overwrite.
	t.Run("every decline adds a row", func(t *testing.T) {
		const orgID = "00000000-0000-0000-0000-0000000000c3"
		seedTTFVOrg(t, orgID, "ttfv-repeat", 40)

		for range 3 {
			if _, err := models.CreateTTFVSurveyResponse(models.DB, &models.TTFVSurveyResponse{
				OrgID: orgID, UserID: "admin@hoop.dev",
			}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		}
		if got := countTTFVResponses(t, orgID); got != 3 {
			t.Errorf("stored %d rows, want 3", got)
		}
	})

	t.Run("a second yes is rejected", func(t *testing.T) {
		const orgID = "00000000-0000-0000-0000-0000000000c5"
		seedTTFVOrg(t, orgID, "ttfv-second-yes", 5)
		activity := "reviewed-recorded-session"

		if _, err := models.CreateTTFVSurveyResponse(models.DB, &models.TTFVSurveyResponse{
			OrgID: orgID, UserID: "admin@hoop.dev", ReachedValue: true, Activity: &activity,
		}); err != nil {
			t.Fatalf("first answer: %v", err)
		}
		// A decline after the yes is refused too: the survey is over for the
		// organization, not merely answered positively once.
		if _, err := models.CreateTTFVSurveyResponse(models.DB, &models.TTFVSurveyResponse{
			OrgID: orgID, UserID: "other-admin@hoop.dev",
		}); !errors.Is(err, models.ErrAlreadyExists) {
			t.Errorf("expected models.ErrAlreadyExists, got %v", err)
		}
		if got := countTTFVResponses(t, orgID); got != 1 {
			t.Errorf("stored %d rows, want 1", got)
		}
	})

}

// Two tabs answering "yes" at the same moment must not leave the organization
// with two contradictory TTFV measurements. The conditional insert alone does
// not prevent that — under READ COMMITTED both submissions read an organization
// that has not confirmed yet — so the guarantee rests on the partial unique
// index plus GORM reporting its violation as gorm.ErrDuplicatedKey, which is
// what CreateTTFVSurveyResponse turns into ErrAlreadyExists.
//
// Both halves are asserted here with sequential writes that bypass the
// conditional insert, because the embedded database serializes every client
// onto a single session and cannot host a real race (see gateway/pglite). The
// race itself is therefore not reproducible in this suite; what is reproducible
// is that the invariant it relies on exists and surfaces the expected error.
func TestTTFVConfirmedValueIsUniquePerOrg(t *testing.T) {
	startTestDB(t)

	const orgID = "00000000-0000-0000-0000-0000000000c6"
	seedTTFVOrg(t, orgID, "ttfv-unique-yes", 15)

	insertAnswer := func(reachedValue bool) error {
		return models.DB.Exec(`
			INSERT INTO private.ttfv_survey_responses (org_id, user_id, reached_value)
			VALUES (?, 'admin@hoop.dev', ?)`, orgID, reachedValue).Error
	}
	if err := insertAnswer(true); err != nil {
		t.Fatalf("first confirmed value: %v", err)
	}
	// Declines are deliberately outside the index: the response history is
	// append-only and only the confirmation is one-per-organization.
	for range 2 {
		if err := insertAnswer(false); err != nil {
			t.Fatalf("decline alongside a confirmed value: %v", err)
		}
	}
	if got := countTTFVResponses(t, orgID); got != 3 {
		t.Errorf("stored %d rows, want 3", got)
	}

	// Last, because a failed statement leaves the embedded database's single
	// shared session unable to serve the next one ("cannot drop active portal").
	// That is a property of the pglite bridge, not of this table.
	if err := insertAnswer(true); !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("expected gorm.ErrDuplicatedKey from the partial unique index, got %v", err)
	}
}

func countTTFVResponses(t *testing.T, orgID string) int64 {
	t.Helper()
	var count int64
	if err := models.DB.Raw(
		`SELECT count(*) FROM private.ttfv_survey_responses WHERE org_id = ?`,
		orgID).Scan(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}
