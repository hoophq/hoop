package apirunbooks

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/runbooks"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// ListRunbooks
//
//	@Summary		List Runbooks
//	@Description	List all Runbooks
//	@Tags			Runbooks
//	@Produce		json
//	@Success		200			{object}	openapi.RunbookList
//	@Failure		404,422,500	{object}	openapi.HTTPError
//	@Router			/runbooks [get]
func ListRunbooksV2(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	runbookConfig, err := models.GetRunbookConfigurationByOrgID(models.DB, ctx.GetOrgID())
	if err != nil {
		log.Infof("failed fetching runbook configuration, err=%v", err)
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "failed fetching runbook configuration"})
		return
	}

	runbookRules, err := models.GetRunbookRules(models.DB, ctx.OrgID, 0, 0)
	if err != nil {
		log.Infof("failed fetching runbook rules, err=%v", err)
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "failed fetching runbook rules"})
		return
	}

	connectionNames, err := models.ListConnectionsName(models.DB, ctx.GetOrgID())
	if err != nil {
		log.Infof("failed fetching connection names, err=%v", err)
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "failed fetching connection names"})
		return
	}

	runbookList := &openapi.RunbookListV2{}
	for _, configEnvVars := range runbookConfig.RepositoryConfigs {
		config, err := runbooks.NewConfigV2(configEnvVars)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
			return
		}
		repositoryList, err := listRunbookFilesV2(ctx.OrgID, config, runbookRules, connectionNames, ctx.UserGroups)
		if err != nil {
			log.Infof("failed listing runbooks, err=%v", err)
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("failed listing runbooks, reason=%v", err)})
			return
		}
		runbookList.Repositories = append(runbookList.Repositories, *repositoryList)
	}

	c.JSON(http.StatusOK, runbookList)
}

// GetRunbookConfiguration
//
//	@Summary		Get Runbook Configuration
//	@Description	Get Runbook Configuration
//	@Tags			Runbooks
//	@Accept			json
//	@Produce		json {object}	openapi.RunbookConfiguration
//	@Success		200			{object}	openapi.RunbookConfiguration
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/runbooks/configurations [get]
func GetRunbookConfiguration(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	runbooks, err := models.GetRunbookConfigurationByOrgID(models.DB, ctx.GetOrgID())
	if err != nil {
		log.Infof("failed fetching runbook configuration, err=%v", err)
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "failed fetching runbook configuration"})
		return
	}

	c.JSON(200, buildRunbookConfigurationResponse(runbooks))
}

// UpdateRunbookConfiguration
//
//	@Summary		Update Runbook Configuration
//	@Description	Update Runbook Configuration
//	@Tags			Runbooks
//	@Accept			json
//	@Produce		json {object}	openapi.RunbookConfiguration
//	@Param			runbook	body		openapi.RunbookConfigurationRequest	true	"Runbook Configuration"
//	@Success		200			{object}	openapi.RunbookConfiguration
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/runbooks/configurations [put]
func UpdateRunbookConfiguration(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	var req openapi.RunbookConfigurationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid request body"})
		return
	}

	repositoryConfigs := make(map[string]map[string]string)

	for _, repo := range req.Repositories {
		mapConfig := buildConfigMapRepository(&repo)
		config, err := runbooks.NewConfigV2(mapConfig)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("failed creating runbook config, reason=%v", err)})
			return
		}

		if _, isset := repositoryConfigs[config.GetNormalizedGitURL()]; isset {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "duplicate git repository URLs are not allowed"})
			return
		}

		repositoryConfigs[config.GetNormalizedGitURL()] = mapConfig
	}

	runbooks := models.Runbooks{
		ID:                uuid.NewString(),
		OrgID:             ctx.GetOrgID(),
		RepositoryConfigs: repositoryConfigs,
	}

	err := models.UpsertRunbookConfiguration(models.DB, &runbooks)
	if err != nil {
		log.Errorf("failed upserting runbook, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed upserting runbook"})
		return
	}

	clearRunbooksCache(ctx.GetOrgID())

	c.JSON(200, buildRunbookConfigurationResponse(&runbooks))
}

func buildConfigMapRepository(config *openapi.RunbookRepository) map[string]string {
	configs := make(map[string]string)

	configs["GIT_URL"] = config.GitUrl
	configs["GIT_USER"] = config.GitUser
	configs["GIT_PASSWORD"] = config.GitPassword
	configs["SSH_KEY"] = config.SSHKey
	configs["SSH_USER"] = config.SSHUser
	configs["SSH_KEY_PASS"] = config.SSHKeyPass
	configs["SSH_KNOWN_HOSTS"] = config.SSHKnownHosts

	return configs
}

func buildRunbookConfigurationResponse(r *models.Runbooks) *openapi.RunbookConfiguration {
	repositories := make([]openapi.RunbookRepository, 0, len(r.RepositoryConfigs))
	for _, repoConfig := range r.RepositoryConfigs {
		repo := openapi.RunbookRepository{
			GitUrl:        repoConfig["GIT_URL"],
			GitUser:       repoConfig["GIT_USER"],
			GitPassword:   repoConfig["GIT_PASSWORD"],
			SSHKey:        repoConfig["SSH_KEY"],
			SSHUser:       repoConfig["SSH_USER"],
			SSHKeyPass:    repoConfig["SSH_KEY_PASS"],
			SSHKnownHosts: repoConfig["SSH_KNOWN_HOSTS"],
		}
		repositories = append(repositories, repo)
	}

	return &openapi.RunbookConfiguration{
		ID:           r.ID,
		OrgID:        r.OrgID,
		Repositories: repositories,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
}
