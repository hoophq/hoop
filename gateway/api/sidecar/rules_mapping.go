package apisidecar

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

type RuleMappingRequest struct {
	RuleType     string `json:"rule_type" binding:"required"` // "guardrail", "datamasking", "ai_analyzer"
	RuleID       string `json:"rule_id" binding:"required"`
	ListenerName string `json:"listener_name" binding:"required"`
}

type RuleMappingInfo struct {
	RuleID       string    `json:"rule_id"`
	RuleName     string    `json:"rule_name"`
	ListenerName string    `json:"listener_name"`
	CreatedAt    time.Time `json:"created_at"`
}

type ListRuleMappingsResponse struct {
	Guardrail   []RuleMappingInfo `json:"guardrail"`
	DataMasking []RuleMappingInfo `json:"datamasking"`
	AIAnalyzer  []RuleMappingInfo `json:"ai_analyzer"`
}

func CreateRuleMapping(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid org id"})
		return
	}

	sidecar, err := models.GetSidecarByNameOrID(models.DB, ctx.GetOrgID(), c.Param("nameOrID"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "sidecar not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to get sidecar")
		return
	}

	var req RuleMappingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	sidecarUUID, err := uuid.Parse(sidecar.ID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "invalid sidecar id in db")
		return
	}

	switch req.RuleType {
	case "guardrail":
		// Validate guardrail rule exists
		_, err := models.GetGuardRailRules(orgID.String(), req.RuleID)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusBadRequest, gin.H{"message": "guardrail rule not found"})
				return
			}
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed validating guardrail rule")
			return
		}

		mapping := models.GuardRailRuleSidecar{
			ID:           uuid.NewString(),
			OrgID:        orgID.String(),
			RuleID:       req.RuleID,
			SidecarID:    sidecarUUID.String(),
			ListenerName: req.ListenerName,
			CreatedAt:    time.Now().UTC(),
		}
		if err := models.DB.Create(&mapping).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				c.JSON(http.StatusConflict, gin.H{"message": "this rule mapping already exists"})
				return
			}
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to create mapping")
			return
		}

	case "datamasking":
		// Validate data masking rule exists
		_, err := models.GetDataMaskingRuleByID(orgID.String(), req.RuleID)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusBadRequest, gin.H{"message": "data masking rule not found"})
				return
			}
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed validating data masking rule")
			return
		}

		mapping := models.DataMaskingRuleSidecar{
			ID:           uuid.NewString(),
			OrgID:        orgID.String(),
			RuleID:       req.RuleID,
			SidecarID:    sidecarUUID.String(),
			ListenerName: req.ListenerName,
			CreatedAt:    time.Now().UTC(),
		}
		if err := models.DB.Create(&mapping).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				c.JSON(http.StatusConflict, gin.H{"message": "this rule mapping already exists"})
				return
			}
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to create mapping")
			return
		}

	case "ai_analyzer":
		// Validate AI session analyzer rule exists
		// Try parsing rule ID as UUID, otherwise lookup by name
		var analyzerRule *models.AISessionAnalyzerRules
		if _, uuidErr := uuid.Parse(req.RuleID); uuidErr == nil {
			err = models.DB.Where("org_id = ? AND id = ?", orgID, req.RuleID).First(&analyzerRule).Error
		} else {
			analyzerRule, err = models.GetAISessionAnalyzerRule(orgID, req.RuleID)
		}
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusBadRequest, gin.H{"message": "AI analyzer rule not found"})
				return
			}
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed validating AI analyzer rule")
			return
		}

		mapping := models.AISessionAnalyzerRuleSidecar{
			ID:             uuid.NewString(),
			OrgID:          orgID.String(),
			AnalyzerRuleID: analyzerRule.ID.String(),
			SidecarID:      sidecarUUID.String(),
			ListenerName:   req.ListenerName,
			CreatedAt:      time.Now().UTC(),
		}
		if err := models.DB.Create(&mapping).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				c.JSON(http.StatusConflict, gin.H{"message": "this rule mapping already exists"})
				return
			}
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to create mapping")
			return
		}

	default:
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid rule type: must be 'guardrail', 'datamasking', or 'ai_analyzer'"})
		return
	}

	c.Writer.WriteHeader(http.StatusCreated)
}

func DeleteRuleMapping(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID := ctx.GetOrgID()

	sidecar, err := models.GetSidecarByNameOrID(models.DB, orgID, c.Param("nameOrID"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "sidecar not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to get sidecar")
		return
	}

	var req RuleMappingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	var dbResult *gorm.DB
	switch req.RuleType {
	case "guardrail":
		dbResult = models.DB.Where("org_id = ? AND rule_id = ? AND sidecar_id = ? AND listener_name = ?",
			orgID, req.RuleID, sidecar.ID, req.ListenerName).Delete(&models.GuardRailRuleSidecar{})
	case "datamasking":
		dbResult = models.DB.Where("org_id = ? AND rule_id = ? AND sidecar_id = ? AND listener_name = ?",
			orgID, req.RuleID, sidecar.ID, req.ListenerName).Delete(&models.DataMaskingRuleSidecar{})
	case "ai_analyzer":
		dbResult = models.DB.Where("org_id = ? AND analyzer_rule_id = ? AND sidecar_id = ? AND listener_name = ?",
			orgID, req.RuleID, sidecar.ID, req.ListenerName).Delete(&models.AISessionAnalyzerRuleSidecar{})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid rule type: must be 'guardrail', 'datamasking', or 'ai_analyzer'"})
		return
	}

	if dbResult.Error != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, dbResult.Error, "failed to delete rule mapping")
		return
	}

	if dbResult.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"message": "rule mapping not found"})
		return
	}

	c.Writer.WriteHeader(http.StatusNoContent)
}

func ListRuleMappings(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID := ctx.GetOrgID()

	sidecar, err := models.GetSidecarByNameOrID(models.DB, orgID, c.Param("nameOrID"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "sidecar not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to get sidecar")
		return
	}

	var grMappings []models.GuardRailRuleSidecar
	if err := models.DB.Where("org_id = ? AND sidecar_id = ?", orgID, sidecar.ID).Order("created_at DESC").Find(&grMappings).Error; err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to query guardrail mappings")
		return
	}

	var dmMappings []models.DataMaskingRuleSidecar
	if err := models.DB.Where("org_id = ? AND sidecar_id = ?", orgID, sidecar.ID).Order("created_at DESC").Find(&dmMappings).Error; err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to query datamasking mappings")
		return
	}

	var aiMappings []models.AISessionAnalyzerRuleSidecar
	if err := models.DB.Where("org_id = ? AND sidecar_id = ?", orgID, sidecar.ID).Order("created_at DESC").Find(&aiMappings).Error; err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to query AI analyzer mappings")
		return
	}

	resp := ListRuleMappingsResponse{
		Guardrail:   make([]RuleMappingInfo, 0, len(grMappings)),
		DataMasking: make([]RuleMappingInfo, 0, len(dmMappings)),
		AIAnalyzer:  make([]RuleMappingInfo, 0, len(aiMappings)),
	}

	for _, m := range grMappings {
		var name string
		_ = models.DB.Table("private.guardrail_rules").Where("org_id = ? AND id = ?", orgID, m.RuleID).Select("name").Scan(&name).Error
		resp.Guardrail = append(resp.Guardrail, RuleMappingInfo{
			RuleID:       m.RuleID,
			RuleName:     name,
			ListenerName: m.ListenerName,
			CreatedAt:    m.CreatedAt,
		})
	}

	for _, m := range dmMappings {
		var name string
		_ = models.DB.Table("private.datamasking_rules").Where("org_id = ? AND id = ?", orgID, m.RuleID).Select("name").Scan(&name).Error
		resp.DataMasking = append(resp.DataMasking, RuleMappingInfo{
			RuleID:       m.RuleID,
			RuleName:     name,
			ListenerName: m.ListenerName,
			CreatedAt:    m.CreatedAt,
		})
	}

	for _, m := range aiMappings {
		var name string
		_ = models.DB.Table("private.ai_session_analyzer_rules").Where("org_id = ? AND id = ?", orgID, m.AnalyzerRuleID).Select("name").Scan(&name).Error
		resp.AIAnalyzer = append(resp.AIAnalyzer, RuleMappingInfo{
			RuleID:       m.AnalyzerRuleID,
			RuleName:     name,
			ListenerName: m.ListenerName,
			CreatedAt:    m.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, resp)
}

func ListRuleMappingsByRule(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	orgID := ctx.GetOrgID()
	ruleID := c.Query("rule_id")
	ruleType := c.Query("rule_type")

	if ruleID == "" || ruleType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "missing rule_id or rule_type"})
		return
	}

	type SidecarListenerMappingInfo struct {
		SidecarID    string `json:"sidecar_id"`
		SidecarName  string `json:"sidecar_name"`
		ListenerName string `json:"listener_name"`
	}

	var results []SidecarListenerMappingInfo

	switch ruleType {
	case "guardrail":
		_ = models.DB.Table("private.guardrail_rules_sidecars grs").
			Select("grs.sidecar_id, s.name as sidecar_name, grs.listener_name").
			Joins("JOIN private.sidecars s ON s.id = grs.sidecar_id").
			Where("grs.org_id = ? AND grs.rule_id = ?", orgID, ruleID).
			Scan(&results).Error
	case "datamasking":
		_ = models.DB.Table("private.datamasking_rules_sidecars dms").
			Select("dms.sidecar_id, s.name as sidecar_name, dms.listener_name").
			Joins("JOIN private.sidecars s ON s.id = dms.sidecar_id").
			Where("dms.org_id = ? AND dms.rule_id = ?", orgID, ruleID).
			Scan(&results).Error
	case "ai_analyzer":
		_ = models.DB.Table("private.ai_session_analyzer_rules_sidecars ars").
			Select("ars.sidecar_id, s.name as sidecar_name, ars.listener_name").
			Joins("JOIN private.sidecars s ON s.id = ars.sidecar_id").
			Where("ars.org_id = ? AND ars.analyzer_rule_id = ?", orgID, ruleID).
			Scan(&results).Error
	}

	c.JSON(http.StatusOK, results)
}
