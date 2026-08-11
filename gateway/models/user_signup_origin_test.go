package models_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/gorm"
)

const testOrgID = "00000000-0000-0000-0000-0000000000a1"

// startTestDB boots the embedded database, applies the migrations and points
// models.DB at it, exactly like the gateway does on startup. The signup origin
// helpers run raw SQL against the real schema, so an in-memory fake would not
// exercise what actually breaks: the interval arithmetic and the conditional
// update.
func startTestDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("start embedded database: %v", err)
	}
	t.Cleanup(func() { inst.Close(ctx) })

	if err := modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}
	// The embedded backend serves one session at a time.
	if err := models.InitDatabaseConnection(inst.DSN(), 1); err != nil {
		t.Fatalf("open gorm connection: %v", err)
	}
	if err := models.DB.Exec(
		`INSERT INTO private.orgs (id, name) VALUES (?, 'origin-survey-test')`, testOrgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
}

// seedUser creates an active user whose record was created createdDaysAgo days
// ago, so the survey window can be exercised without waiting.
func seedUser(t *testing.T, subject string, createdDaysAgo float64) {
	t.Helper()
	err := models.DB.Exec(`
		INSERT INTO private.users (org_id, subject, email, name, status, created_at)
		VALUES (?, ?, ?, ?, 'active', NOW() - make_interval(mins => ?))`,
		testOrgID, subject, subject+"@hoop.dev", subject, int(createdDaysAgo*24*60),
	).Error
	if err != nil {
		t.Fatalf("seed user %s: %v", subject, err)
	}
}

func TestShouldShowOriginSurvey(t *testing.T) {
	startTestDB(t)

	answered := "user-answered"
	seedUser(t, answered, 1)
	if err := models.SetUserSignupOrigin(models.DB, testOrgID, answered, "referral", nil); err != nil {
		t.Fatalf("record answer: %v", err)
	}

	// Bracket the 7 day boundary from both sides without landing on it, so the
	// assertions cannot flake on clock resolution.
	seedUser(t, "user-fresh", 0)
	seedUser(t, "user-inside-window", 6.95)
	seedUser(t, "user-outside-window", 7.05)

	tests := []struct {
		subject string
		expect  bool
	}{
		{subject: "user-fresh", expect: true},
		{subject: "user-inside-window", expect: true},
		{subject: "user-outside-window", expect: false},
		{subject: answered, expect: false},
	}
	for _, tt := range tests {
		t.Run(tt.subject, func(t *testing.T) {
			got, err := models.ShouldShowOriginSurvey(models.DB, testOrgID, tt.subject)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expect {
				t.Errorf("expected %v, got %v", tt.expect, got)
			}
		})
	}

	t.Run("unknown user propagates not found", func(t *testing.T) {
		_, err := models.ShouldShowOriginSurvey(models.DB, testOrgID, "nobody")
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Errorf("expected gorm.ErrRecordNotFound, got %v", err)
		}
	})
}

func TestSetUserSignupOrigin(t *testing.T) {
	startTestDB(t)

	t.Run("stores the answer and its free text", func(t *testing.T) {
		seedUser(t, "user-other", 0)
		detail := "Saw it in a conference talk"
		if err := models.SetUserSignupOrigin(models.DB, testOrgID, "user-other", "other", &detail); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		origin, other := readAnswer(t, "user-other")
		if origin != "other" || other != detail {
			t.Errorf("expected (other, %q), got (%q, %q)", detail, origin, other)
		}
	})

	t.Run("leaves the free text null for other options", func(t *testing.T) {
		seedUser(t, "user-plain", 0)
		if err := models.SetUserSignupOrigin(models.DB, testOrgID, "user-plain", "ai-discovery", nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var otherIsNull bool
		if err := models.DB.Raw(
			`SELECT signup_origin_other IS NULL FROM private.users WHERE org_id = ? AND subject = 'user-plain'`,
			testOrgID).Scan(&otherIsNull).Error; err != nil {
			t.Fatalf("read answer: %v", err)
		}
		if !otherIsNull {
			t.Error("expected signup_origin_other to stay null")
		}
	})

	// "1 answer per user": the second submission must not overwrite the first.
	t.Run("refuses a second answer", func(t *testing.T) {
		seedUser(t, "user-twice", 0)
		if err := models.SetUserSignupOrigin(models.DB, testOrgID, "user-twice", "referral", nil); err != nil {
			t.Fatalf("first answer: %v", err)
		}
		err := models.SetUserSignupOrigin(models.DB, testOrgID, "user-twice", "social-media", nil)
		if !errors.Is(err, models.ErrAlreadyExists) {
			t.Fatalf("expected models.ErrAlreadyExists, got %v", err)
		}
		if origin, _ := readAnswer(t, "user-twice"); origin != "referral" {
			t.Errorf("expected the first answer to win, got %q", origin)
		}
	})

	t.Run("unknown user propagates not found", func(t *testing.T) {
		err := models.SetUserSignupOrigin(models.DB, testOrgID, "nobody", "referral", nil)
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Errorf("expected gorm.ErrRecordNotFound, got %v", err)
		}
	})
}

func readAnswer(t *testing.T, subject string) (origin, other string) {
	t.Helper()
	var row struct {
		SignupOrigin      string
		SignupOriginOther string
	}
	err := models.DB.Raw(`
		SELECT COALESCE(signup_origin, '') AS signup_origin,
		       COALESCE(signup_origin_other, '') AS signup_origin_other
		FROM private.users WHERE org_id = ? AND subject = ?`, testOrgID, subject).Scan(&row).Error
	if err != nil {
		t.Fatalf("read answer for %s: %v", subject, err)
	}
	return row.SignupOrigin, row.SignupOriginOther
}
