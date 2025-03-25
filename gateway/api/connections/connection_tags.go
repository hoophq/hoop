package apiconnections

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// TODO(san): needs more testing, will add these endpoints later on
// func CreateTag(c *gin.Context) {
// 	ctx := storagev2.ParseContext(c)
// 	var req openapi.ConnectionTagCreateRequest
// 	if err := c.ShouldBindJSON(&req); err != nil {
// 		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
// 		return
// 	}

// 	obj := &models.ConnectionTag{
// 		OrgID:     ctx.OrgID,
// 		ID:        uuid.NewString(),
// 		Key:       req.Key,
// 		Value:     req.Value,
// 		CreatedAt: time.Now().UTC(),
// 		UpdatedAt: time.Now().UTC(),
// 	}
// 	err := models.CreateConnectionTag(obj)
// 	switch err {
// 	case models.ErrAlreadyExists:
// 		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
// 	case nil:
// 		c.JSON(http.StatusCreated, obj)
// 	default:
// 		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
// 	}
// }

// func UpdateTagByID(c *gin.Context) {
// 	ctx := storagev2.ParseContext(c)
// 	var req openapi.ConnectionTagUpdateRequest
// 	if err := c.ShouldBindJSON(&req); err != nil {
// 		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
// 		return
// 	}
// 	resourceID := c.Param("id")
// 	obj, err := models.GetConnectionTagByID(ctx.OrgID, resourceID)
// 	switch err {
// 	case models.ErrNotFound:
// 		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
// 	case nil:
// 		err := models.UpdateConnectionTagValue(ctx.OrgID, resourceID, req.Value)
// 		if err != nil {
// 			log.Errorf("failed updating connection tags, reason=%v", err)
// 			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
// 			return
// 		}
// 		obj.Value = req.Value
// 		c.JSON(http.StatusOK, obj)
// 	default:
// 		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
// 	}

// }

// func GetTagByID(c *gin.Context) {
// 	ctx := storagev2.ParseContext(c)
// 	obj, err := models.GetConnectionTagByID(ctx.OrgID, c.Param("id"))
// 	switch err {
// 	case models.ErrNotFound:
// 		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
// 	case nil:
// 		c.JSON(http.StatusOK, obj)
// 	default:
// 		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
// 	}
// }

// List Connection Tags
//
//	@Summary		List Connection Tags
//	@Description	List all Connection Tags.
//	@Tags			Connections
//	@Produce		json
//	@Success		200	{array}		openapi.ConnectionTag
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/connections-tags [get]
func ListTags(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	items, err := models.ListConnectionTags(ctx.OrgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	var result openapi.ConnectionTagList
	for _, c := range items {
		result.Items = append(result.Items, toConnectionTags(c))
	}

	c.JSON(http.StatusOK, result)
}

func toConnectionTags(c models.ConnectionTag) openapi.ConnectionTag {
	return openapi.ConnectionTag{
		ID:        c.ID,
		Key:       c.Key,
		Value:     c.Value,
		UpdatedAt: c.UpdatedAt,
		CreatedAt: c.CreatedAt,
	}
}
