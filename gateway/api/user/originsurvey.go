package userapi

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

// signupOriginOther is the single option that also carries free text.
const signupOriginOther = "other"

// validSignupOrigins are the accepted answers of the onboarding
// "How did you hear about Hoop?" survey. These identifiers are the analytics
// contract: renaming one breaks the acquisition-channel reports, so add new
// options instead of repurposing existing ones. The user-facing labels live in
// the webapp.
var validSignupOrigins = []string{
	"search-engine",
	"ai-discovery",
	"referral",
	"already-in-use-at-company",
	"tech-community",
	"social-media",
	"hoop-free-tools",
	signupOriginOther,
}

// maxSignupOriginOtherLength matches the users.signup_origin_other column width
// so an oversized payload fails with a 400 instead of a database error.
const maxSignupOriginOtherLength = 255

// PostSignupOrigin
//
//	@Summary		Answer Signup Origin Survey
//	@Description	Record how the authenticated user heard about Hoop. Each user may answer only once; a second attempt returns 409.
//	@Tags			User Management
//	@Accept			json
//	@Produce		json
//	@Param			request				body	openapi.UserSignupOriginRequest	true	"The request body resource"
//	@Success		204					"Answer recorded"
//	@Failure		400,403,404,409,500	{object}	openapi.HTTPError
//	@Router			/users/self/signup-origin [post]
func PostSignupOrigin(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	// Anonymous users have no record in the users table yet, so there is
	// nothing to attach the answer to. The survey is offered after signup.
	if ctx.IsAnonymous() {
		c.JSON(http.StatusForbidden, gin.H{"message": "the signup must be completed before answering the survey"})
		return
	}

	var req openapi.UserSignupOriginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if !slices.Contains(validSignupOrigins, req.Origin) {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "invalid origin, accepted values are " + strings.Join(validSignupOrigins, ", "),
		})
		return
	}

	// The free text belongs to the 'other' option only. For any other answer it
	// is dropped rather than rejected, so the stored columns always agree on
	// which option was picked regardless of what the client kept in its state.
	var otherText *string
	if req.Origin == signupOriginOther {
		detail := strings.TrimSpace(req.OriginOther)
		switch {
		case detail == "":
			c.JSON(http.StatusBadRequest, gin.H{"message": "origin_other is required when origin is 'other'"})
			return
		case len(detail) > maxSignupOriginOtherLength:
			c.JSON(http.StatusBadRequest, gin.H{"message": "origin_other must not exceed 255 characters"})
			return
		}
		otherText = &detail
	}

	err := models.SetUserSignupOrigin(models.DB, ctx.OrgID, ctx.UserID, req.Origin, otherText)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "user not found"})
		return
	case errors.Is(err, models.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"message": "the signup origin survey was already answered"})
		return
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed recording the signup origin answer")
		return
	}

	// The free text is intentionally left out of the event: it is user supplied
	// and may contain personal data, which would bypass the organization's
	// analytics mode. Read it from the database when the answers are reviewed.
	trackClient := analytics.New()
	defer trackClient.Close()
	trackClient.Track(ctx.UserID, analytics.EventOnboardingOriginAnswered, map[string]any{
		"org-id": ctx.OrgID,
		"origin": req.Origin,
	})

	c.Status(http.StatusNoContent)
}
