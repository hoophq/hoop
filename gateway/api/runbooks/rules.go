package apirunbooks

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// ListRunbookRules
//
// @Summary      List Runbook Rules
// @Description  List all Runbook Rules
// @Tags         Runbooks
// @Produce      json
// @Success      200  {object}  []openapi.RunbookRule
// @Failure      404,422,500  {object} openapi.HTTPError
// @Router       /runbooks/rules [get]
func ListRunbookRules(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	rules, err := models.GetRunbookRules(models.DB, ctx.GetOrgID(), 0, 0)
	if err != nil {
		log.Infof("failed fetching runbook rules, err=%v", err)
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "failed fetching runbook rules"})
		return
	}

	responseRules := make([]openapi.RunbookRule, 0, len(rules))
	for _, rule := range rules {
		responseRules = append(responseRules, buildRunbookResponseFromModel(&rule))
	}

	c.JSON(http.StatusOK, responseRules)
}

// GetRunbookRule
//
// @Summary      Get Runbook Rule
// @Description  Get a single Runbook Rule by ID
// @Tags         Runbooks
// @Produce      json
// @Param        id  path      string  true  "Runbook Rule ID"
// @Success      200  {object}  openapi.RunbookRule
// @Failure      404,422,500  {object} openapi.HTTPError
// @Router       /runbooks/rules/{id} [get]
func GetRunbookRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	ruleID := c.Param("id")

	rule, err := models.GetRunbookRuleByID(models.DB, ctx.GetOrgID(), ruleID)
	if err != nil {
		log.Infof("failed fetching runbook rule, err=%v", err)
		c.JSON(http.StatusNotFound, gin.H{"message": "runbook rule not found"})
		return
	}

	responseRule := buildRunbookResponseFromModel(rule)
	c.JSON(http.StatusOK, responseRule)
}

// CreateRunbookRule
//
// @Summary      Create Runbook Rule
// @Description  Create a new Runbook Rule
// @Tags         Runbooks
// @Accept       json
// @Produce      json
// @Param        rule  body     openapi.RunbookRule  true  "Runbook Rule"
// @Success      201  {object}  openapi.RunbookRule
// @Failure      400,422,500  {object} openapi.HTTPError
// @Router       /runbooks/rules [post]
func CreateRunbookRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	var reqRule openapi.RunbookRule
	if err := c.ShouldBindJSON(&reqRule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid request body"})
		return
	}

	rule := buildRunbookRuleModelFromRequest(&reqRule)
	rule.OrgID = ctx.GetOrgID()

	if err := models.UpsertRunbookRule(models.DB, &rule); err != nil {
		log.Errorf("failed creating runbook rule, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed creating runbook rule"})
		return
	}

	ruleRes := buildRunbookResponseFromModel(&rule)

	c.JSON(http.StatusCreated, ruleRes)
}

// UpdateRunbookRule
//
// @Summary      Update Runbook Rule
// @Description  Update an existing Runbook Rule by ID
// @Tags         Runbooks
// @Accept       json
// @Produce      json
// @Param        id    path      string              true  "Runbook Rule ID"
// @Param        rule  body      openapi.RunbookRule  true  "Runbook Rule"
// @Success      200  {object}   openapi.RunbookRule
// @Failure      400,404,422,500  {object} openapi.HTTPError
// @Router       /runbooks/rules/{id} [put]
func UpdateRunbookRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	ruleID := c.Param("id")

	var reqRule openapi.RunbookRule
	if err := c.ShouldBindJSON(&reqRule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid request body"})
		return
	}

	rule := buildRunbookRuleModelFromRequest(&reqRule)
	rule.ID = ruleID
	rule.OrgID = ctx.GetOrgID()

	if err := models.UpsertRunbookRule(models.DB, &rule); err != nil {
		log.Errorf("failed updating runbook rule, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed updating runbook rule"})
		return
	}

	ruleRes := buildRunbookResponseFromModel(&rule)

	c.JSON(http.StatusOK, ruleRes)
}

// DeleteRunbookRule
//
// @Summary      Delete Runbook Rule
// @Description  Delete a Runbook Rule by ID
// @Tags         Runbooks
// @Produce      json
// @Param        id  path      string  true  "Runbook Rule ID"
// @Success      204  {object}  nil
// @Failure      404,422,500  {object} openapi.HTTPError
// @Router       /runbooks/rules/{id} [delete]
func DeleteRunbookRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	ruleID := c.Param("id")

	if err := models.DeleteRunbookRule(models.DB, ctx.GetOrgID(), ruleID); err != nil {
		log.Errorf("failed deleting runbook rule, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed deleting runbook rule"})
		return
	}

	c.Status(http.StatusNoContent)
}

func buildRunbookResponseFromModel(rule *models.RunbookRules) openapi.RunbookRule {
	runbooks := make([]openapi.RunbookRuleFile, 0, len(rule.Runbooks))
	for _, r := range rule.Runbooks {
		runbooks = append(runbooks, openapi.RunbookRuleFile{
			Repository: r.Repository,
			Name:       r.Name,
		})
	}

	return openapi.RunbookRule{
		ID:          rule.ID,
		OrgID:       rule.OrgID,
		Name:        rule.Name,
		Description: rule.Description.String,
		UserGroups:  rule.UserGroups,
		Connections: rule.Connections,
		Runbooks:    runbooks,
		CreatedAt:   rule.CreatedAt,
		UpdatedAt:   rule.UpdatedAt,
	}
}

func buildRunbookRuleModelFromRequest(reqRule *openapi.RunbookRule) models.RunbookRules {
	id := reqRule.ID
	if id == "" {
		id = uuid.NewString()
	}

	runbooks := make([]models.RunbookRuleFile, 0, len(reqRule.Runbooks))
	for _, r := range reqRule.Runbooks {
		runbooks = append(runbooks, models.RunbookRuleFile{
			Repository: r.Repository,
			Name:       r.Name,
		})
	}

	return models.RunbookRules{
		ID:          id,
		OrgID:       reqRule.OrgID,
		Name:        reqRule.Name,
		Description: sql.NullString{String: reqRule.Description, Valid: reqRule.Description != ""},
		UserGroups:  reqRule.UserGroups,
		Connections: reqRule.Connections,
		Runbooks:    runbooks,
	}
}
