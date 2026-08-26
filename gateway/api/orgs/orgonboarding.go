package apiorgs

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

// GetOrgOnboarding
//
//	@Summary		Get Organization Onboarding Status
//	@Description	Get the setup checklist state for the caller's organization. Each step latches the first time it is satisfied, so a check never reverts. Once every check passes, `show_setup_checklist` on /userinfo turns false — the signal to stop calling this endpoint.
//	@Tags			Server Management
//	@Produce		json
//	@Success		200	{object}	openapi.OrgOnboardingResponse
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/orgs/onboarding [get]
func GetOrgOnboarding(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	status, err := services.SyncOrgOnboardingStatus(models.DB, ctx.OrgID, types.GroupAdmin)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to load onboarding status: %v", err)
		return
	}

	c.JSON(http.StatusOK, openapi.OrgOnboardingResponse{
		Completed: status.Completed,
		Checks: openapi.OrgOnboardingChecks{
			AgentDeployed:       status.Checks[models.StepAgentDeployed],
			ResourceCreated:     status.Checks[models.StepResourceCreated],
			SessionRan:          status.Checks[models.StepSessionRan],
			GroupsCreated:       status.Checks[models.StepGroupsCreated],
			PeopleAssigned:      status.Checks[models.StepPeopleAssigned],
			GuardrailsExplored:  status.Checks[models.StepGuardrailsExplored],
			DataMaskingExplored: status.Checks[models.StepDataMaskingExplored],
			AIAnalyzerEnabled:   status.Checks[models.StepAIAnalyzerEnabled],
			ProtectionLevelSet:  status.Checks[models.StepProtectionLevelSet],
		},
		ExecConnectionName:  status.ExecConnectionName,
		FirstConnectionName: status.FirstConnectionName,
	})
}
