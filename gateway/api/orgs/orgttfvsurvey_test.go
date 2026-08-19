package apiorgs

import (
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
)

func ptr[T any](v T) *T { return &v }

func TestValidateTTFVAnswer(t *testing.T) {
	for _, tc := range []struct {
		name              string
		req               openapi.TTFVSurveyRequest
		wantErr           bool
		wantActivity      *string
		wantActivityOther *string
	}{
		{
			name: "a confirmed value keeps its activity",
			req: openapi.TTFVSurveyRequest{
				ReachedValue: ptr(true),
				Activity:     "saw-guardrail-applied",
			},
			wantActivity: ptr("saw-guardrail-applied"),
		},
		{
			// A decline is the answer that has to keep working: it is what
			// starts the cooldown instead of closing the survey.
			name:         "a decline needs nothing else",
			req:          openapi.TTFVSurveyRequest{ReachedValue: ptr(false)},
			wantActivity: nil,
		},
		{
			// Whatever the client kept in its state for the branch not taken is
			// dropped, so the stored row cannot describe two answers at once.
			name: "a decline drops the activity it was sent with",
			req: openapi.TTFVSurveyRequest{
				ReachedValue:  ptr(false),
				Activity:      "other",
				ActivityOther: "Configured SSO",
			},
			wantActivity:      nil,
			wantActivityOther: nil,
		},
		{
			name: `"other" carries its free text`,
			req: openapi.TTFVSurveyRequest{
				ReachedValue:  ptr(true),
				Activity:      "other",
				ActivityOther: "  Configured SSO  ",
			},
			wantActivity:      ptr("other"),
			wantActivityOther: ptr("Configured SSO"),
		},
		{
			name: "free text is dropped for every other activity",
			req: openapi.TTFVSurveyRequest{
				ReachedValue:  ptr(true),
				Activity:      "reviewed-recorded-session",
				ActivityOther: "ignored",
			},
			wantActivity:      ptr("reviewed-recorded-session"),
			wantActivityOther: nil,
		},
		{
			name:    "a confirmed value without an activity is rejected",
			req:     openapi.TTFVSurveyRequest{ReachedValue: ptr(true)},
			wantErr: true,
		},
		{
			name: "an unknown activity is rejected",
			req: openapi.TTFVSurveyRequest{
				ReachedValue: ptr(true),
				Activity:     "wrote-a-poem",
			},
			wantErr: true,
		},
		{
			name: `"other" without free text is rejected`,
			req: openapi.TTFVSurveyRequest{
				ReachedValue:  ptr(true),
				Activity:      "other",
				ActivityOther: "   ",
			},
			wantErr: true,
		},
		{
			name: "free text past the limit is rejected",
			req: openapi.TTFVSurveyRequest{
				ReachedValue:  ptr(true),
				Activity:      "other",
				ActivityOther: strings.Repeat("a", maxTTFVActivityOtherLength+1),
			},
			wantErr: true,
		},
		{
			// Measured in characters, not bytes: 255 multi-byte characters fit
			// the contract even though they are far more than 255 bytes.
			name: "free text is measured in characters",
			req: openapi.TTFVSurveyRequest{
				ReachedValue:  ptr(true),
				Activity:      "other",
				ActivityOther: strings.Repeat("é", maxTTFVActivityOtherLength),
			},
			wantActivity:      ptr("other"),
			wantActivityOther: ptr(strings.Repeat("é", maxTTFVActivityOtherLength)),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			answer, err := validateTTFVAnswer(tc.req)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got answer %+v", answer)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if answer.ReachedValue != *tc.req.ReachedValue {
				t.Errorf("ReachedValue = %v, want %v", answer.ReachedValue, *tc.req.ReachedValue)
			}
			assertOptionalString(t, "Activity", answer.Activity, tc.wantActivity)
			assertOptionalString(t, "ActivityOther", answer.ActivityOther, tc.wantActivityOther)
		})
	}
}

// The event is the half of the measurement that product dashboards read, and
// losing a property from it is silent — no build breaks, no other test fails,
// the metric just goes to zero. So the payload is asserted key by key.
func TestTTFVEventProperties(t *testing.T) {
	orgCreatedAt := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	measured := &models.TTFVMeasurement{
		OrgCreatedAt: &orgCreatedAt,
		TTFVSeconds:  ptr(int64(864000)),
	}

	t.Run("a confirmed value reports the activity and the duration", func(t *testing.T) {
		props := ttfvEventProperties("org-1", services.TTFVSurveyAnswer{
			ReachedValue: true,
			Activity:     ptr("saw-guardrail-applied"),
		}, measured)

		assertProp(t, props, "org-id", "org-1")
		assertProp(t, props, "reached-value", true)
		assertProp(t, props, "activity", "saw-guardrail-applied")
		assertProp(t, props, "org-created-at", "2026-07-01T12:00:00Z")
		assertProp(t, props, "ttfv-seconds", int64(864000))
	})

	// The free text is the one field that may carry personal data, so it must
	// never reach an analytics destination — the database keeps it instead.
	t.Run("the free text never leaves the database", func(t *testing.T) {
		props := ttfvEventProperties("org-1", services.TTFVSurveyAnswer{
			ReachedValue:  true,
			Activity:      ptr("other"),
			ActivityOther: ptr("Rotated the production credentials"),
		}, measured)

		for key, value := range props {
			if s, ok := value.(string); ok && strings.Contains(s, "Rotated") {
				t.Fatalf("property %q leaked the free text: %v", key, value)
			}
		}
		if _, exists := props["activity-other"]; exists {
			t.Error("activity-other must not be sent")
		}
		if _, exists := props["activity_other"]; exists {
			t.Error("activity_other must not be sent")
		}
	})

	t.Run("a decline reports no activity", func(t *testing.T) {
		props := ttfvEventProperties("org-1", services.TTFVSurveyAnswer{ReachedValue: false}, measured)

		assertProp(t, props, "reached-value", false)
		if _, exists := props["activity"]; exists {
			t.Error("a decline has no activity to report")
		}
	})

	// An org with no creation timestamp has no duration; reporting one anyway
	// would enter the dashboards as a TTFV of zero and understate the metric.
	t.Run("an unmeasurable organization reports no duration", func(t *testing.T) {
		props := ttfvEventProperties("org-1",
			services.TTFVSurveyAnswer{ReachedValue: true, Activity: ptr("other")},
			&models.TTFVMeasurement{})

		for _, key := range []string{"org-created-at", "ttfv-seconds"} {
			if _, exists := props[key]; exists {
				t.Errorf("%s must be absent when the organization has no creation time", key)
			}
		}
	})

	// Every other event this gateway emits uses kebab-case. A stray snake_case
	// property is not a style problem downstream, it is a second spelling of
	// the same field that has to be reconciled in every report.
	t.Run("property names are kebab-case", func(t *testing.T) {
		props := ttfvEventProperties("org-1", services.TTFVSurveyAnswer{
			ReachedValue: true,
			Activity:     ptr("other"),
		}, measured)

		for key := range props {
			if strings.Contains(key, "_") {
				t.Errorf("property %q uses snake_case", key)
			}
		}
	})
}

func assertProp(t *testing.T, props map[string]any, key string, want any) {
	t.Helper()
	got, exists := props[key]
	if !exists {
		t.Errorf("property %q is missing", key)
		return
	}
	if got != want {
		t.Errorf("property %q = %v (%T), want %v (%T)", key, got, got, want, want)
	}
}

// reached_value is a *bool with binding:"required" precisely so that a "no"
// survives binding. A plain bool would make false indistinguishable from an
// omitted field, and required would then reject every decline — the survey
// would become terminal on the first one, which is the opposite of the policy.
// The activity identifiers exist in three places: this validator, the enums tag
// on openapi.TTFVSurveyRequest that publishes them, and constants.js in the
// webapp that renders them. Nothing links the three, and every way they can
// drift is silent — an option the widget offers but the API rejects is a 400 the
// user sees as a broken form, and one the API accepts but no report knows about
// is a row nobody counts. So the list is pinned literally here, and the tag is
// asserted to agree with it. Changing an identifier should mean changing this
// test, the webapp, and saying so on the ticket.
func TestTTFVActivityContract(t *testing.T) {
	want := []string{
		"saw-guardrail-applied",
		"saw-data-masked",
		"approved-or-denied-access-request",
		"reviewed-recorded-session",
		"opened-ai-analyzed-session-report",
		"other",
	}
	if !slices.Equal(validTTFVActivities, want) {
		t.Errorf("validTTFVActivities = %v, want %v", validTTFVActivities, want)
	}

	// The published contract is the struct tag, not the slice: the frontend
	// reads the generated OpenAPI, so a tag that disagrees is a documented lie.
	field, ok := reflect.TypeOf(openapi.TTFVSurveyRequest{}).FieldByName("Activity")
	if !ok {
		t.Fatal("openapi.TTFVSurveyRequest has no Activity field")
	}
	if got := strings.Split(field.Tag.Get("enums"), ","); !slices.Equal(got, want) {
		t.Errorf("enums tag = %v, want %v", got, want)
	}
}

func TestTTFVSurveyRequestBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "an explicit no binds", body: `{"reached_value":false}`},
		{name: "an explicit yes binds", body: `{"reached_value":true,"activity":"other"}`},
		{name: "an omitted answer is rejected", body: `{"activity":"other"}`, wantErr: true},
		{name: "a null answer is rejected", body: `{"reached_value":null}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/orgs/ttfv-survey", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")

			var req openapi.TTFVSurveyRequest
			err := c.ShouldBindJSON(&req)
			switch {
			case tc.wantErr && err == nil:
				t.Fatal("expected a binding error")
			case !tc.wantErr && err != nil:
				t.Fatalf("unexpected binding error: %v", err)
			}
		})
	}
}

func assertOptionalString(t *testing.T, field string, got, want *string) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil || want == nil:
		t.Errorf("%s = %v, want %v", field, derefOrNil(got), derefOrNil(want))
	case *got != *want:
		t.Errorf("%s = %q, want %q", field, *got, *want)
	}
}

func derefOrNil(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}
