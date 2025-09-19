package apiconnections

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// UpdateDataMaskingRuleConnection
//
//	@Summary		Update Data Masking Rule Connections
//	@Description	Update Data Masking Rule Connections
//	@Tags			Connections
//	@Accept			json
//	@Produce		json
//	@Param			request		body		[]openapi.DataMaskingRuleConnectionRequest	true	"The request body resource"
//	@Success		200			{object}	[]openapi.DataMaskingRuleConnection
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/datamasking-rules [put]
func UpdateDataMaskingRuleConnection(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req []openapi.DataMaskingRuleConnectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	conn, err := models.GetConnectionByNameOrID(ctx, c.Param("nameOrID"))
	if err != nil {
		log.Errorf("failed fetching connection, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
		return
	}

	httpResponseItems := []openapi.DataMaskingRuleConnection{}
	var dbItems []models.DataMaskingRuleConnection
	for _, reqItem := range req {
		resource := models.DataMaskingRuleConnection{
			ID:           uuid.NewString(),
			OrgID:        ctx.GetOrgID(),
			RuleID:       reqItem.RuleID,
			ConnectionID: conn.ID,
			Status:       reqItem.Status,
		}
		dbItems = append(dbItems, resource)
		httpResponseItems = append(httpResponseItems, openapi.DataMaskingRuleConnection{
			ID:           resource.ID,
			RuleID:       resource.RuleID,
			ConnectionID: resource.ConnectionID,
			Status:       resource.Status,
		})
	}
	_, err = models.UpdateDataMaskingRuleConnection(ctx.GetOrgID(), conn.ID, dbItems)
	if err != nil {
		log.Errorf("failed updating data masking rule connection, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, httpResponseItems)
}
