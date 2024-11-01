package apiguardrails

import (
	"fmt"
	"libhoop/log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// CreateGuardRailRules
//
//	@Summary		Create Guard Rail Rules
//	@Description	Create Guard Rail Rules
//	@Tags			Core
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.GuardRailRule	true	"The request body resource"
//	@Success		201				{object}	openapi.GuardRailRule
//	@Failure		400,409,422,500	{object}	openapi.HTTPError
//	@Router			/guardrail-rules [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	req := parseRequestPayload(c)
	if req == nil {
		return
	}

	rule := &models.GuardRailRules{
		ID:        uuid.NewString(),
		OrgID:     ctx.GetOrgID(),
		Name:      req.Name,
		Input:     req.Input,
		Output:    req.Output,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	err := models.CreateGuardRailRules(rule)
	if err != nil {
		log.Errorf("failed creting guard rail rule, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	toJSON(c, http.StatusCreated, rule)
}

// UpdateGuardRailRules
//
//	@Summary		Update Guard Rail Rules
//	@Description	Update Guard Rail Rules
//	@Tags			Core
//	@Accept			json
//	@Produce		json
//	@Param			request	body		openapi.GuardRailRule	true	"The request body resource"
//	@Success		200		{object}	openapi.GuardRailRule
//	@Failure		409,500	{object}	openapi.HTTPError
//	@Router			/guardrail-rules/{id} [put]
func Put(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	req := parseRequestPayload(c)
	if req == nil {
		return
	}
	rule := &models.GuardRailRules{
		OrgID:     ctx.GetOrgID(),
		ID:        c.Param("id"),
		Name:      req.Name,
		Input:     req.Input,
		Output:    req.Output,
		UpdatedAt: time.Now().UTC(),
	}
	err := models.UpsertGuardRailRules(rule)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
		return
	case nil:
		toJSON(c, http.StatusOK, rule)
	default:
		log.Errorf("failed updating guard rail rule, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

// ListGuardRailRules
//
//	@Summary		List Guard Rail Rules
//	@Description	List Guard Rail Rules
//	@Tags			Core
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.GuardRailRule	true	"The request body resource"
//	@Success		200				{array}		openapi.GuardRailRule
//	@Failure		400,409,422,500	{object}	openapi.HTTPError
//	@Router			/guardrail-rules [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	ruleList, err := models.ListGuardRailRules(ctx.GetOrgID())
	if err != nil {
		log.Errorf("failed listing guard rail rules, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	toJSON(c, http.StatusOK, ruleList)
}

// GetGuardRailRules
//
//	@Summary		Get Guard Rail Rules
//	@Description	Get Guard Rail Rules
//	@Tags			Core
//	@Accept			json
//	@Produce		json
//	@Param			id				path		string					true	"The unique identifier of the resource"
//	@Param			request			body		openapi.GuardRailRule	true	"The request body resource"
//	@Success		200				{object}	openapi.GuardRailRule
//	@Failure		400,409,422,500	{object}	openapi.HTTPError
//	@Router			/guardrail-rules/{id} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	rule, err := models.GetGuardRailRules(ctx.GetOrgID(), c.Param("id"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		toJSON(c, http.StatusOK, rule)
	default:
		log.Errorf("failed listing guard rail rules, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

// DeleteConnection
//
//	@Summary		Delete a Rule
//	@Description	Delete a Guard Rail Rule resource.
//	@Tags			Core
//	@Produce		json
//	@Param			id	path	string	true	"The unique identifier of the resource"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/guardrail-rules/{id} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	err := models.DeleteGuardRailRules(ctx.GetOrgID(), c.Param("id"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		log.Errorf("failed removing guard rail rules, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

func parseRequestPayload(c *gin.Context) *openapi.GuardRailRuleRequest {
	req := openapi.GuardRailRuleRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Errorf("failed parsing request payload, err=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return nil
	}
	return &req
}

func toJSON(c *gin.Context, statusCode int, obj any) {
	switch r := obj.(type) {
	case *models.GuardRailRules:
		c.JSON(statusCode, &openapi.GuardRailRuleResponse{
			ID:        r.ID,
			Name:      r.Name,
			Input:     r.Input,
			Output:    r.Output,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
		})
	case []*models.GuardRailRules:
		var rules []openapi.GuardRailRuleResponse
		for _, rule := range r {
			rules = append(rules, openapi.GuardRailRuleResponse{
				ID:        rule.ID,
				Name:      rule.Name,
				Input:     rule.Input,
				Output:    rule.Output,
				CreatedAt: rule.CreatedAt,
				UpdatedAt: rule.UpdatedAt,
			})
		}
		c.JSON(statusCode, rules)
	default:
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": fmt.Sprintf("unable to parse response obj %T", obj)})

	}
}
