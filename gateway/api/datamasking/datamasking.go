package apigdatamasking

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// CreateDataMaskingRule
//
//	@Summary		Create Data Masking Rule
//	@Description	Create Data Masking Rule
//	@Tags			Data Masking Rules
//	@Accept			json
//	@Produce		json
//	@Param			request		body		openapi.DataMaskingRuleRequest	true	"The request body resource"
//	@Success		201			{object}	openapi.DataMaskingRule
//	@Failure		400,409,500	{object}	openapi.HTTPError
//	@Router			/datamasking-rules [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	req := parseRequestPayload(c)
	if req == nil {
		return
	}

	supportedEntityTypes := []models.SupportedEntityTypesEntry{}
	for _, entityType := range req.SupportedEntityTypes {
		supportedEntityTypes = append(supportedEntityTypes, models.SupportedEntityTypesEntry{
			Name:        entityType.Name,
			EntityTypes: entityType.EntityTypes,
		})
	}
	customEntityTypes := []models.CustomEntityTypesEntry{}
	for _, entityType := range req.CustomEntityTypesEntrys {
		customEntityTypes = append(customEntityTypes, models.CustomEntityTypesEntry{
			Name:     entityType.Name,
			Regex:    entityType.Regex,
			DenyList: entityType.DenyList,
			Score:    entityType.Score,
		})
	}

	rule, err := models.CreateDataMaskingRule(&models.DataMaskingRule{
		ID:                   uuid.NewString(),
		OrgID:                ctx.OrgID,
		Name:                 req.Name,
		Description:          req.Description,
		SupportedEntityTypes: supportedEntityTypes,
		CustomEntityTypes:    customEntityTypes,
		ScoreThreshold:       req.ScoreThreshold,
		ConnectionIDs:        req.ConnectionIDs,
		UpdatedAt:            time.Now().UTC(),
	})

	switch err {
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
	case models.ErrNotFound:
		c.JSON(http.StatusBadRequest, gin.H{"message": "connection not found: a connection reference in the connection_ids field does not exist"})
	case nil:
		c.JSON(http.StatusCreated, toOpenApi(rule))
	default:
		log.Errorf("Failed creating data masking rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

// UpdateDataMaskingRule
//
//	@Summary		Update Data Masking Rule
//	@Description	Update Data Masking Rule
//	@Tags			Data Masking Rules
//	@Accept			json
//	@Produce		json
//	@Param			request		body		openapi.DataMaskingRuleRequest	true	"The request body resource"
//	@Success		200			{object}	openapi.DataMaskingRule
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/datamasking-rules/{id} [put]
func Put(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	req := parseRequestPayload(c)
	if req == nil {
		return
	}

	supportedEntityTypes := []models.SupportedEntityTypesEntry{}
	for _, entityType := range req.SupportedEntityTypes {
		supportedEntityTypes = append(supportedEntityTypes, models.SupportedEntityTypesEntry{
			Name:        entityType.Name,
			EntityTypes: entityType.EntityTypes,
		})
	}
	customEntityTypes := []models.CustomEntityTypesEntry{}
	for _, entityType := range req.CustomEntityTypesEntrys {
		customEntityTypes = append(customEntityTypes, models.CustomEntityTypesEntry{
			Name:     entityType.Name,
			Regex:    entityType.Regex,
			DenyList: entityType.DenyList,
			Score:    entityType.Score,
		})
	}

	rule, err := models.UpdateDataMaskingRule(&models.DataMaskingRule{
		ID:                   c.Param("id"),
		OrgID:                ctx.GetOrgID(),
		Name:                 req.Name,
		Description:          req.Description,
		SupportedEntityTypes: supportedEntityTypes,
		CustomEntityTypes:    customEntityTypes,
		ScoreThreshold:       req.ScoreThreshold,
		ConnectionIDs:        req.ConnectionIDs,
		UpdatedAt:            time.Now().UTC(),
	})

	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
	case nil:
		c.JSON(http.StatusOK, toOpenApi(rule))
	default:
		log.Errorf("Failed updating data masking rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

// ListDataMaskingRules
//
//	@Summary		List Data Masking Rules
//	@Description	List Data Masking Rules
//	@Tags			Data Masking Rules
//	@Accept			json
//	@Produce		json
//	@Success		200	{array}		openapi.DataMaskingRule
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/datamasking-rules [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	ruleList, err := models.ListDataMaskingRules(ctx.GetOrgID())
	if err != nil {
		log.Errorf("failed listing data masking rules, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	rules := []openapi.DataMaskingRule{}
	for _, rule := range ruleList {
		rules = append(rules, *toOpenApi(&rule))
	}
	c.JSON(http.StatusOK, rules)
}

// GetDataMaskingRule
//
//	@Summary		Get Data Masking Rule
//	@Description	Get Data Masking Rule
//	@Tags			Data Masking Rules
//	@Accept			json
//	@Produce		json
//	@Param			id			path		string	true	"The unique identifier of the resource"
//	@Success		200			{object}	openapi.DataMaskingRule
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/datamasking-rules/{id} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	rule, err := models.GetDataMaskingRuleByID(ctx.GetOrgID(), c.Param("id"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.JSON(http.StatusOK, toOpenApi(rule))
	default:
		log.Errorf("failed fetching data masking rule, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

// DeleteDataMaskingRule
//
//	@Summary		Delete Data Masking Rule
//	@Description	Delete a Data Masking Rule resource.
//	@Tags			Data Masking Rules
//	@Produce		json
//	@Param			id	path	string	true	"The unique identifier of the resource"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/datamasking-rules/{id} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	err := models.DeleteDataMaskingRule(ctx.GetOrgID(), c.Param("id"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		log.Errorf("failed removing data masking rule, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

func toOpenApi(obj *models.DataMaskingRule) *openapi.DataMaskingRule {
	entityTypes := []openapi.SupportedEntityTypesEntry{}
	for _, entry := range obj.SupportedEntityTypes {
		entityTypes = append(entityTypes, openapi.SupportedEntityTypesEntry{
			Name:        entry.Name,
			EntityTypes: entry.EntityTypes,
		})
	}
	customEntityTypes := []openapi.CustomEntityTypesEntry{}
	for _, entry := range obj.CustomEntityTypes {
		customEntityTypes = append(customEntityTypes, openapi.CustomEntityTypesEntry{
			Name:     entry.Name,
			Regex:    entry.Regex,
			DenyList: entry.DenyList,
			Score:    entry.Score,
		})
	}

	return &openapi.DataMaskingRule{
		ID: obj.ID,
		DataMaskingRuleRequest: openapi.DataMaskingRuleRequest{
			Name:                    obj.Name,
			Description:             obj.Description,
			SupportedEntityTypes:    entityTypes,
			CustomEntityTypesEntrys: customEntityTypes,
			ScoreThreshold:          obj.ScoreThreshold,
			ConnectionIDs:           obj.ConnectionIDs,
			UpdatedAt:               obj.UpdatedAt,
		},
	}
}

func parseRequestPayload(c *gin.Context) *openapi.DataMaskingRuleRequest {
	req := openapi.DataMaskingRuleRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Errorf("failed parsing request payload, err=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return nil
	}
	if req.ScoreThreshold != nil && *req.ScoreThreshold < 0 && *req.ScoreThreshold > 1 {
		c.JSON(http.StatusBadRequest, gin.H{"message": "score threshold must be between 0 and 1"})
		return nil
	}
	for _, connID := range req.ConnectionIDs {
		if _, err := uuid.Parse(connID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": "invalid connection ID " + connID})
			return nil
		}
	}
	for _, e := range req.SupportedEntityTypes {
		if len(e.EntityTypes) == 0 || e.Name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"message": "missing entity types or name in supported entity types"})
			return nil
		}
		if e.Name != strings.ToUpper(e.Name) {
			c.JSON(http.StatusBadRequest, gin.H{"message": "entity type name must be uppercase: " + e.Name})
			return nil
		}
	}
	for _, e := range req.CustomEntityTypesEntrys {
		if e.Name == "" || len(e.DenyList) == 0 && e.Regex == "" {
			c.JSON(http.StatusBadRequest, gin.H{"message": "missing name, deny_list or regex in custom entity types"})
			return nil
		}
		if e.Name != strings.ToUpper(e.Name) {
			c.JSON(http.StatusBadRequest, gin.H{"message": "entity type name must be uppercase: " + e.Name})
			return nil
		}
	}
	return &req
}
