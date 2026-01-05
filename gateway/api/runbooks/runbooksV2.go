package apirunbooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/common/runbooks"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/openapi"
	sessionapi "github.com/hoophq/hoop/gateway/api/session"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/jira"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

// ListRunbooks
//
//	@Summary		List Runbooks
//	@Description	List all Runbooks
//	@Tags			Runbooks
//	@Produce		json
//	@Param			connection_name	query		string	false	"Filter runbooks by connection name"
//	@Success		200				{object}	openapi.RunbookListV2
//	@Failure		404,500			{object}	openapi.HTTPError
//	@Router			/runbooks [get]
func ListRunbooksV2(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	runbookConfig, err := models.GetRunbookConfigurationByOrgID(models.DB, ctx.GetOrgID())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "runbook configuration not found"})
			return
		}

		log.Errorf("failed fetching runbook configuration, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed fetching runbook configuration, reason=%v", err)})
		return
	}

	runbookRules, err := models.GetRunbookRules(models.DB, ctx.OrgID, 0, 0)
	if err != nil {
		log.Errorf("failed fetching runbook rules, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed fetching runbook rules, reason=%v", err)})
		return
	}

	urlQuery := c.Request.URL.Query()
	connectionName := urlQuery.Get("connection_name")

	removeEmptyConnectionsList := true
	connectionNames := []string{connectionName}

	if connectionName == "" {
		removeEmptyConnectionsList = false
		connectionNames, err = models.ListConnectionsName(models.DB, ctx.GetOrgID())
		if err != nil {
			log.Errorf("failed fetching connection names, err=%v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed fetching connection names, reason=%v", err)})
			return
		}
	}

	runbookList := &openapi.RunbookListV2{
		Errors:       make([]string, 0),
		Repositories: []openapi.RunbookRepositoryList{},
	}
	for _, repoConfig := range runbookConfig.RepositoryConfigs {
		config, err := models.BuildCommonConfig(&repoConfig)
		if err != nil {
			runbookList.Errors = append(runbookList.Errors, fmt.Sprintf("failed creating runbook config for repo %s, err=%v", repoConfig.GitUrl, err))
			continue
		}

		repositoryList, err := listRunbookFilesV2(ctx.OrgID, config, runbookRules, connectionNames, ctx.UserGroups, removeEmptyConnectionsList)
		if err != nil {
			runbookList.Errors = append(runbookList.Errors, fmt.Sprintf("failed listing runbooks for repo %s, err=%v", repoConfig.GitUrl, err))
			continue
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
//	@Produce		json
//	@Success		200		{object}	openapi.RunbookConfigurationResponse
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/runbooks/configurations [get]
func GetRunbookConfiguration(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	runbooks, err := models.GetRunbookConfigurationByOrgID(models.DB, ctx.GetOrgID())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "runbook configuration not found"})
			return
		}

		log.Errorf("failed fetching runbook configuration, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching runbook configuration"})
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
//	@Produce		json
//	@Param			request			body		openapi.RunbookConfigurationRequest	true	"Runbook Configuration"
//	@Success		200				{object}	openapi.RunbookConfigurationResponse
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/runbooks/configurations [put]
func UpdateRunbookConfiguration(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	var req openapi.RunbookConfigurationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("invalid request body, reason=%v", err)})
		return
	}

	repositoryConfigs := make(map[string]models.RunbookRepositoryConfig)
	for _, repo := range req.Repositories {
		mapConfig := buildConfigMapRepository(&repo)
		config, err := models.BuildCommonConfig(&mapConfig)
		if err != nil {
			log.Errorf("failed creating runbook config, reason=%v", err)
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("failed creating runbook config, reason=%v", err)})
			return
		}

		if _, isset := repositoryConfigs[config.GetNormalizedGitURL()]; isset {
			c.JSON(http.StatusBadRequest, gin.H{"message": "duplicate git repository URLs are not allowed"})
			return
		}

		repositoryConfigs[config.GetNormalizedGitURL()] = mapConfig
	}

	runbooks := models.Runbooks{
		ID:                uuid.NewString(), // May be ignored in upsert
		OrgID:             ctx.GetOrgID(),
		RepositoryConfigs: repositoryConfigs,
	}

	err := models.UpsertRunbookConfiguration(models.DB, &runbooks)
	if err != nil {
		log.Errorf("failed upserting runbook, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed upserting runbook, reason=%v", err)})
		return
	}

	deleteRunbookCache(ctx.GetOrgID(), "") // clear all cache for this org

	c.JSON(200, buildRunbookConfigurationResponse(&runbooks))
}

// CreateRunbookConfigurationEntry
//
//	@Summary		Create Runbook Configuration Entry
//	@Description	Create Runbook Configuration Entry
//	@Tags			Runbooks
//	@Accept			json
//	@Produce		json
//	@Param			request		body		openapi.RunbookRepository	true	"Runbook Repository Configuration"
//	@Success		200			{object}	openapi.RunbookConfigurationResponse
//	@Failure		409,422,500	{object}	openapi.HTTPError
//	@Router			/runbooks/configurations [post]
func CreateRunbookConfigurationEntry(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	req, err := parseRunbookRepoRequest(c)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	dbConfig := buildConfigMapRepository(req)
	config, err := models.BuildCommonConfig(&dbConfig)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("failed creating runbook config, reason=%v", err)})
		return
	}

	err = models.CreateRunbookConfigurationEntry(models.DB, ctx.OrgID, config.GetNormalizedGitURL(), &dbConfig)
	switch err {
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
	case nil:
		c.JSON(http.StatusCreated, buildRunbookRepositoryResponse(config.GetNormalizedGitURL(), &dbConfig))
	default:
		errMsg := fmt.Sprintf("failed creating runbook configuration, reason=%v", err)
		log.Error(errMsg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
	}
}

// UpdateRunbookConfigurationEntry
//
//	@Summary		Update Runbook Configuration Entry
//	@Description	Update Runbook Configuration Entry
//	@Tags			Runbooks
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.RunbookRepository	true	"Runbook Repository Configuration"
//	@Success		200				{object}	openapi.RunbookConfigurationResponse
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/runbooks/configurations/{id} [put]
func UpdateRunbookConfigurationEntry(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	req, err := parseRunbookRepoRequest(c)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	// a check to ensure the git url in the path and body match
	gitUrlID := c.Param("id")
	if gitUrlID != uuid.NewSHA1(uuid.NameSpaceURL, []byte(req.GitUrl)).String() {
		c.JSON(http.StatusBadRequest, gin.H{"message": "git url in the path and body do not match"})
		return
	}

	dbConfig := buildConfigMapRepository(req)
	config, err := models.BuildCommonConfig(&dbConfig)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("failed creating runbook config, reason=%v", err)})
		return
	}

	err = models.UpdateRunbookConfigurationEntry(models.DB, ctx.OrgID, config.GetNormalizedGitURL(), &dbConfig)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
	case nil:
		deleteRunbookCache(ctx.GetOrgID(), "") // clear all cache for this org
		c.JSON(http.StatusOK, buildRunbookRepositoryResponse(config.GetNormalizedGitURL(), &dbConfig))
	default:
		errMsg := fmt.Sprintf("failed updating runbook configuration, reason=%v", err)
		log.Error(errMsg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
	}
}

// DeleteRunbookConfigurationEntry
//
//	@Summary		Delete Runbook Configuration Entry
//	@Description	Delete a runbook configuration entry.
//	@Tags			Runbooks
//	@Produce		json
//	@Param			id	path	string	true	"The id of the resource"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/runbooks/configurations/{id} [delete]
func DeleteRunbookConfiguration(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	id := c.Param("id")

	err := models.DeleteRunbookConfigurationEntry(models.DB, ctx.OrgID, id)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
	case nil:
		deleteRunbookCache(ctx.GetOrgID(), "") // clear all cache for this org
		c.Status(http.StatusNoContent)
	default:
		errMsg := fmt.Sprintf("failed deleting runbook configuration, reason=%v", err)
		log.Error(errMsg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
	}
}

func buildConfigMapRepository(config *openapi.RunbookRepository) models.RunbookRepositoryConfig {
	return models.RunbookRepositoryConfig{
		GitUrl:        config.GitUrl,
		GitUser:       config.GitUser,
		GitPassword:   config.GitPassword,
		SSHKey:        config.SSHKey,
		SSHUser:       config.SSHUser,
		SSHKeyPass:    config.SSHKeyPass,
		SSHKnownHosts: config.SSHKnownHosts,
		GitHookTTL:    config.GitHookTTL,
	}
}

func buildRunbookRepositoryResponse(repository string, repoConfig *models.RunbookRepositoryConfig) *openapi.RunbookRepositoryResponse {
	return &openapi.RunbookRepositoryResponse{
		Repository:    repository,
		GitUrl:        repoConfig.GitUrl,
		GitUser:       repoConfig.GitUser,
		GitPassword:   repoConfig.GitPassword,
		SSHKey:        repoConfig.SSHKey,
		SSHUser:       repoConfig.SSHUser,
		SSHKeyPass:    repoConfig.SSHKeyPass,
		SSHKnownHosts: repoConfig.SSHKnownHosts,
		GitHookTTL:    repoConfig.GitHookTTL,
	}
}

func buildRunbookConfigurationResponse(r *models.Runbooks) *openapi.RunbookConfigurationResponse {
	repositories := make([]openapi.RunbookRepositoryResponse, 0, len(r.RepositoryConfigs))
	for repository, repoConfig := range r.RepositoryConfigs {
		repo := buildRunbookRepositoryResponse(repository, &repoConfig)
		repositories = append(repositories, *repo)
	}

	return &openapi.RunbookConfigurationResponse{
		ID:           r.ID,
		OrgID:        r.OrgID,
		Repositories: repositories,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
}

// RunRunbookExec
//
//	@Summary		Runbook Exec
//	@Description	Start a execution using a Runbook as input. If the connection has a JIRA issue template configured, it will create a JIRA issue.
//	@Tags			Runbooks
//	@Accept			json
//	@Produce		json
//	@Param			request				body		openapi.RunbookExec		true	"The request body resource"
//	@Success		200					{object}	openapi.ExecResponse	"The execution has finished"
//	@Success		202					{object}	openapi.ExecResponse	"The execution is still in progress"
//	@Failure		400,403,404,422,500	{object}	openapi.HTTPError
//	@Router			/runbooks/exec [post]
func RunbookExec(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.RunbookExec
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	allowed, err := models.IsUserAllowedToRunRunbook(ctx.OrgID, req.ConnectionName, req.Repository, req.FileName, ctx.UserGroups)
	if err != nil {
		log.Errorf("failed checking user permissions for runbook exec, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"message": "user is not allowed to run this runbook on this connection"})
		return
	}

	if err := sessionapi.CoerceMetadataFields(req.Metadata); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	connectionName := req.ConnectionName
	connection, err := getConnection(ctx, c, connectionName)
	if err != nil {
		log.Warn(err)
		return
	}

	configs, err := models.GetRunbookConfigurationByOrgID(models.DB, ctx.OrgID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "runbook configuration not found"})
			return
		}

		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	repoConfig, ok := configs.RepositoryConfigs[req.Repository]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("runbook repository config %v not found", req.Repository)})
		return
	}

	config, err := models.BuildCommonConfig(&repoConfig)
	if err != nil {
		log.Errorf("failed creating runbook config, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	commit, err := GetRunbooks(ctx.GetOrgID(), config)
	if err != nil {
		log.Errorf("failed cloning runbook repository, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	repo, err := runbooks.BuildRepositoryFromCommit(commit)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	runbook, err := repo.ReadFile(req.FileName, req.Parameters)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if runbook == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": fmt.Sprintf("runbook file %v not found on git repo", req.FileName)})
		return
	}

	for key, val := range req.EnvVars {
		// don't replace environment variables from runbook
		if _, ok := runbook.EnvVars[key]; ok {
			continue
		}
		runbook.EnvVars[key] = val
	}

	runbookParamsJson, _ := json.Marshal(req.Parameters)
	sessionLabels := openapi.SessionLabelsType{
		"runbookRepository": config.GetNormalizedGitURL(),
		"runbookFile":       req.FileName,
		"runbookParameters": string(runbookParamsJson),
	}

	sessionID := uuid.NewString()
	apiroutes.SetSidSpanAttr(c, sessionID)
	userAgent := apiutils.NormalizeUserAgent(c.Request.Header.Values)
	if userAgent == "webapp.core" {
		userAgent = "webapp.runbook.exec"
	}

	client, err := clientexec.New(&clientexec.Options{
		OrgID:          ctx.GetOrgID(),
		SessionID:      sessionID,
		ConnectionName: connectionName,
		BearerToken:    apiroutes.GetAccessTokenFromRequest(c),
		UserAgent:      userAgent,
		Origin:         proto.ConnectionOriginClientAPIRunbooks,
	})
	if err != nil {
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	newSession := models.Session{
		ID:                   sessionID,
		OrgID:                ctx.GetOrgID(),
		Connection:           connectionName,
		ConnectionType:       connection.Type,
		ConnectionSubtype:    connection.SubType.String,
		Verb:                 proto.ClientVerbExec,
		Labels:               sessionLabels,
		Metadata:             req.Metadata,
		IntegrationsMetadata: nil,
		Metrics:              nil,
		BlobInput:            models.BlobInputType(runbook.InputFile),
		UserID:               ctx.UserID,
		UserName:             ctx.UserName,
		UserEmail:            ctx.UserEmail,
		Status:               string(openapi.SessionStatusOpen),
		ExitCode:             nil,
		CreatedAt:            time.Now().UTC(),
		EndSession:           nil,
	}

	if connection.JiraIssueTemplateID.String != "" {
		issueTemplate, jiraConfig, err := models.GetJiraIssueTemplatesByID(connection.OrgID, connection.JiraIssueTemplateID.String)
		if err != nil {
			log.Errorf("failed obtaining jira issue template for %v, reason=%v", connection.Name, err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": fmt.Sprintf("failed obtaining jira issue template: %v", err)})
			return
		}
		if jiraConfig != nil && jiraConfig.IsActive() {
			if req.JiraFields == nil {
				req.JiraFields = map[string]string{}
			}
			jiraFields, err := jira.ParseIssueFields(issueTemplate, req.JiraFields, newSession)
			switch err.(type) {
			case *jira.ErrInvalidIssueFields:
				c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
				return
			case nil:
			default:
				log.Error(err)
				c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
				return
			}
			resp, err := jira.CreateCustomerRequest(issueTemplate, jiraConfig, jiraFields)
			if err != nil {
				log.Error(err)
				c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
				return
			}
			newSession.IntegrationsMetadata = map[string]any{
				"jira_issue_key": resp.IssueKey,
				"jira_issue_url": resp.Links.Agent,
			}
		}
	}

	if err := models.UpsertSession(newSession); err != nil {
		log.Errorf("failed persisting session, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "The session couldn't be created"})
		return
	}

	var params string
	for key, val := range req.Parameters {
		params += fmt.Sprintf("%s:len[%v],", key, len(val))
	}
	log := log.With("sid", sessionID)
	log.Infof("runbook exec, commit=%s, name=%s, connection=%s, parameters=%v",
		runbook.CommitSHA[:8], req.FileName, connectionName, strings.TrimSpace(params))

	respCh := make(chan *clientexec.Response)
	go func() {
		defer func() { close(respCh); client.Close() }()
		select {
		case respCh <- client.Run(runbook.InputFile, runbook.EnvVars, req.ClientArgs...):
		default:
		}
	}()

	timeoutCtx, cancelFn := context.WithTimeout(context.Background(), time.Second*50)
	defer cancelFn()
	select {
	case outcome := <-respCh:
		log.Infof("runbook exec response, %v", outcome)
		c.JSON(http.StatusOK, outcome)
	case <-timeoutCtx.Done():
		client.Close()
		log.Infof("runbook exec timeout (50s), it will return async")
		c.JSON(http.StatusAccepted, clientexec.NewTimeoutResponse(sessionID))
	}
}

func parseRunbookRepoRequest(c *gin.Context) (*openapi.RunbookRepository, error) {
	var req openapi.RunbookRepository
	if err := c.ShouldBindJSON(&req); err != nil {
		return nil, fmt.Errorf("invalid request body, reason=%v", err)
	}

	if err := validateGitURL(req.GitUrl); err != nil {
		return nil, fmt.Errorf("invalid git url, reason=%v", err)
	}
	return &req, nil
}

var sshGitRegex = regexp.MustCompile(`^[\w\-\.]+@[\w\.\-]+:[\w\.\-/~_]+(\.git)?$`)

func validateGitURL(urlStr string) error {
	if urlStr == "" {
		return fmt.Errorf("URL is empty")
	}

	// Check if it's SSH format (git@github.com:user/repo.git)
	if sshGitRegex.MatchString(urlStr) {
		return nil
	}

	// Check if it's a standard URL format
	u, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	validSchemes := map[string]bool{
		"http":  true,
		"https": true,
		"git":   true,
		"ssh":   true,
	}

	if !validSchemes[scheme] {
		return fmt.Errorf("invalid scheme: %s (must be http, https, git, or ssh)", scheme)
	}

	if u.Host == "" {
		return fmt.Errorf("URL must have a host")
	}

	return nil
}
