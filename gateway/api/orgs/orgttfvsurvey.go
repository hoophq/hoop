package apiorgs

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

// ttfvActivityOther is the single option that also carries free text.
const ttfvActivityOther = "other"

// validTTFVActivities are the accepted answers to "what did you get done?".
// These identifiers are the analytics contract and are duplicated in
// webapp_v2/src/features/TtfvSurvey/constants.js: renaming one breaks the TTFV
// reports and the widget at the same time, so add new options instead of
// repurposing existing ones. The user-facing labels live in the webapp.
var validTTFVActivities = []string{
	"connected-infra-resource",
	"approved-or-denied-access-request",
	"reviewed-recorded-session",
	"created-or-activated-policy",
	"opened-ai-analyzed-session-report",
	"set-up-data-masking-rule",
	ttfvActivityOther,
}

// maxTTFVActivityOtherLength bounds the free text. The column is a TEXT, so
// this is a contract limit rather than a storage one: it keeps a pasted
// document out of the response history.
const maxTTFVActivityOtherLength = 255

// validateTTFVAnswer turns a request body into the answer to store, rejecting
// the combinations the contract does not allow.
//
// Fields that do not belong to the chosen answer are dropped rather than
// rejected, so the stored row is always unambiguous about which answer it is
// regardless of what the client kept in its state.
func validateTTFVAnswer(req openapi.TTFVSurveyRequest) (services.TTFVSurveyAnswer, error) {
	// Non-nil is guaranteed by binding:"required" on the pointer, which is
	// what lets an explicit false through where a plain bool could not — and a
	// "no" that cannot be submitted would make the survey terminal on the
	// first decline, the opposite of the policy.
	if !*req.ReachedValue {
		return services.TTFVSurveyAnswer{ReachedValue: false}, nil
	}

	activity := strings.TrimSpace(req.Activity)
	if !slices.Contains(validTTFVActivities, activity) {
		return services.TTFVSurveyAnswer{}, errors.New(
			"invalid activity, accepted values are " + strings.Join(validTTFVActivities, ", "))
	}

	answer := services.TTFVSurveyAnswer{ReachedValue: true, Activity: &activity}
	if activity != ttfvActivityOther {
		return answer, nil
	}

	detail := strings.TrimSpace(req.ActivityOther)
	switch {
	case detail == "":
		return services.TTFVSurveyAnswer{}, errors.New(`activity_other is required when activity is "other"`)
	// Counted in characters, not bytes, so a perfectly storable non-ASCII
	// answer is not turned into a 400 by its UTF-8 width.
	case utf8.RuneCountInString(detail) > maxTTFVActivityOtherLength:
		return services.TTFVSurveyAnswer{}, errors.New("activity_other must not exceed 255 characters")
	}
	answer.ActivityOther = &detail
	return answer, nil
}

// PostOrgTTFVSurvey
//
//	@Summary		Answer TTFV Survey
//	@Description	Record whether an administrator got done what they came to do, and measure the time since the organization was created. A confirmed value is terminal — the organization is never asked again and a second attempt returns 409 — while a decline is recorded and followed by another ask after a cooldown. Dismissing the widget is not an answer and must not be submitted.
//	@Tags			Server Management
//	@Accept			json
//	@Produce		json
//	@Param			request				body	openapi.TTFVSurveyRequest	true	"The request body resource"
//	@Success		204					"Answer recorded"
//	@Failure		400,403,404,409,500	{object}	openapi.HTTPError
//	@Router			/orgs/ttfv-survey [post]
func PostOrgTTFVSurvey(c *gin.Context) {
	// The organization's analytics mode is deliberately not re-checked here. It
	// gates whether the question is asked, and an answer only exists because the
	// widget was rendered, so refusing it would discard a real data point rather
	// than prevent one — an admin who answered and then opted out in another tab
	// would lose the answer they had already given. Consent is still honoured
	// where it counts: analytics.Track drops the event for a disabled
	// organization, so the answer stays in that deployment's own database and
	// never leaves it.
	ctx := storagev2.ParseContext(c)
	// Anonymous users are not the administrator whose confirmation defines
	// TTFV, and have no record to attribute the answer to.
	if ctx.IsAnonymous() {
		c.JSON(http.StatusForbidden, gin.H{"message": "the signup must be completed before answering the survey"})
		return
	}

	var req openapi.TTFVSurveyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	answer, err := validateTTFVAnswer(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	measurement, err := services.RecordTTFVSurveyAnswer(models.DB, ctx.OrgID, ctx.UserID, answer)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "organization not found"})
		return
	case errors.Is(err, models.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"message": "the ttfv survey was already answered for this organization"})
		return
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed recording the ttfv survey answer")
		return
	}

	// Emitted from the gateway rather than the browser: a client-side call is
	// blocked by the same ad blockers that ruled out measuring this through
	// Intercom, which would bias the metric towards the users least likely to
	// run one.
	//
	// The row written above is the system of record; this event is the copy the
	// product dashboards read. It is deliberately best effort — Track drops it
	// when no Segment key is configured or when the organization set its
	// analytics mode to disabled — which is why the duration is recomputable
	// from private.ttfv_survey_responses and does not exist only here.
	trackClient := analytics.New()
	defer trackClient.Close()
	trackClient.Track(ctx.UserID, analytics.EventTTFVSurveyAnswered,
		ttfvEventProperties(ctx.OrgID, answer, measurement))

	c.Status(http.StatusNoContent)
}

// ttfvEventProperties builds the analytics payload for one recorded answer.
//
// Pure and separate from the handler, like identifyTraits in gateway/analytics,
// so a test can hold the event to its contract: a property that quietly stops
// being sent breaks no build and fails no other test, it just makes the metric
// downstream go to zero.
//
// The free text is intentionally absent. It is user supplied and may contain
// personal data, which would bypass the organization's analytics mode. Read it
// from private.ttfv_survey_responses when the answers are reviewed.
func ttfvEventProperties(orgID string, answer services.TTFVSurveyAnswer, measurement *models.TTFVMeasurement) map[string]any {
	// Kebab-case throughout, matching every other event this gateway emits
	// (org-id, session-id, auth-method, client-version). "org-id" in particular
	// is load-bearing rather than stylistic: Segment.Track reads that exact key
	// to resolve the organization's analytics mode and to set $groups.
	properties := map[string]any{
		"org-id":        orgID,
		"reached-value": answer.ReachedValue,
	}
	if answer.Activity != nil {
		properties["activity"] = *answer.Activity
	}
	// Both are absent for an organization with no recorded creation time, which
	// has no duration to report rather than a duration of zero.
	if measurement.OrgCreatedAt != nil {
		properties["org-created-at"] = measurement.OrgCreatedAt.Format(time.RFC3339)
	}
	if measurement.TTFVSeconds != nil {
		properties["ttfv-seconds"] = *measurement.TTFVSeconds
	}
	return properties
}
