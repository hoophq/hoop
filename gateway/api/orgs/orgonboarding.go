package apiorgs

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

// GetOrgOnboarding
//
//	@Summary		Get Organization Onboarding Status
//	@Description	Get the state of the setup checklist for the caller's organization. Once every check passes the completion is latched permanently and `show_setup_checklist` on /userinfo turns false, which is the signal to stop calling this endpoint.
//	@Tags			Server Management
//	@Produce		json
//	@Success		200	{object}	openapi.OrgOnboardingResponse
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/orgs/onboarding [get]
func GetOrgOnboarding(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	status, err := models.GetOrgOnboardingStatus(models.DB, ctx.OrgID, types.GroupAdmin)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to load onboarding status: %v", err)
		return
	}

	// Latch on the read path: the checks span features that have no reason to
	// know a checklist exists, so this is the only place that observes "all
	// steps done". A failed stamp is retried by the next call.
	if status.AllChecksPass() && !status.PreviouslyCompleted {
		if err := models.MarkOrgOnboardingCompleted(models.DB, ctx.OrgID); err != nil {
			log.Warnf("failed stamping onboarding completion for org %s, err=%v", ctx.OrgID, err)
		}
	}

	c.JSON(http.StatusOK, openapi.OrgOnboardingResponse{
		Completed: status.Completed(),
		Checks: openapi.OrgOnboardingChecks{
			AgentDeployed:       status.AgentDeployed,
			ResourceCreated:     status.ResourceCreated,
			SessionRan:          status.SessionRan,
			GroupsCreated:       status.GroupsCreated,
			PeopleAssigned:      status.PeopleAssigned,
			GuardrailsExplored:  status.GuardrailsExplored,
			DataMaskingExplored: status.DataMaskingExplored,
			AIAnalyzerEnabled:   status.AIAnalyzerEnabled,
			ProtectionLevelSet:  status.ProtectionLevelSet,
		},
		ExecConnectionName:  status.ExecConnectionName,
		FirstConnectionName: status.FirstConnectionName,
	})
}
