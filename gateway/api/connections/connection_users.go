package apiconnections

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

// ListUsers
//
//	@Summary		List Connection Users
//	@Description	List the users who can open a connection, and the path that grants each one access. Ordered by email.
//	@Tags			Connections
//	@Produce		json
//	@Param			nameOrID	path		string												true	"Name or UUID of the connection"
//	@Param			page		query		int													false	"Page number (1-based)"							Format(int)
//	@Param			page_size	query		int													false	"Maximum number of items to return (1-100)"	Format(int)
//	@Success		200			{object}	openapi.PaginatedResponse[openapi.ConnectionUser]
//	@Failure		404,422,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/users [get]
func ListUsers(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	page, pageSize, err := apivalidation.ParsePaginationParams(c.Query("page"), c.Query("page_size"))
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	items, total, err := models.ListConnectionUsers(models.DB, ctx.GetOrgID(), c.Param("nameOrID"), page, pageSize)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
		return
	}
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing connection users: %v", err)
		return
	}
	data := make([]openapi.ConnectionUser, len(items))
	for i, item := range items {
		data[i] = openapi.ConnectionUser{
			ID:              item.Subject,
			Name:            item.Name,
			Email:           item.Email,
			Status:          openapi.StatusType(item.Status),
			AccessType:      item.AccessType,
			Groups:          append([]string{}, item.Groups...),
			AttributeGroups: append([]string{}, item.AttributeGroups...),
		}
	}
	c.JSON(http.StatusOK, openapi.PaginatedResponse[openapi.ConnectionUser]{
		Pages: openapi.Pagination{Total: int(total), Page: page, Size: pageSize},
		Data:  data,
	})
}
