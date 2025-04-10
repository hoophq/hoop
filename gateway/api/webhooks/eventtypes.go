package webhooks

import (
	"context"
	"fmt"
	"net/http"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	svix "github.com/svix/svix-webhooks/go"
)

func CreateSvixEventType(c *gin.Context) {
	if !isValidTenancyType(c) {
		return
	}
	svixClient, err := getSvixClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	var req svix.EventTypeIn
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	response, err := svixClient.EventType.Create(context.TODO(), &req)

	if isApiError(c, "event-type-create", "", err) {
		return
	}
	c.JSON(http.StatusCreated, response)
}

func ListSvixEventTypes(c *gin.Context) {
	if !isValidTenancyType(c) {
		return
	}
	svixClient, err := getSvixClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	response, err := svixClient.EventType.List(
		context.Background(),
		&svix.EventTypeListOptions{},
	)
	if isApiError(c, "event-type-list", "", err) {
		return
	}
	c.JSON(http.StatusOK, response)
}

func GetSvixEventTypeByName(c *gin.Context) {
	if !isValidTenancyType(c) {
		return
	}
	eventTypeName := c.Param("name")
	svixClient, err := getSvixClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	response, err := svixClient.EventType.Get(context.Background(), eventTypeName)
	if isApiError(c, "event-type-get", eventTypeName, err) {
		return
	}
	c.JSON(http.StatusOK, response)
}

func UpdateSvixEventType(c *gin.Context) {
	if !isValidTenancyType(c) {
		return
	}
	eventTypeName := c.Param("name")
	svixClient, err := getSvixClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	var req svix.EventTypeUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	response, err := svixClient.EventType.Update(context.TODO(), eventTypeName, &req)
	if isApiError(c, "event-type-update", eventTypeName, err) {
		return
	}
	c.JSON(http.StatusOK, response)

}

func DeleteSvixEventType(c *gin.Context) {
	if !isValidTenancyType(c) {
		return
	}
	eventTypeName := c.Param("name")
	svixClient, err := getSvixClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	options := &svix.EventTypeDeleteOptions{Expunge: ptr.Bool(true)}
	err = svixClient.EventType.DeleteWithOptions(context.Background(), eventTypeName, options)
	if isApiError(c, "event-type-delete", eventTypeName, err) {
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func isValidTenancyType(c *gin.Context) bool {
	if appconfig.Get().OrgMultitenant() {
		c.JSON(http.StatusNotImplemented, gin.H{"message": "endpoint is not allowed for multi tenant setups"})
		return false
	}
	return true
}

func isApiError(c *gin.Context, op, resourceName string, err error) (failed bool) {
	switch e := err.(type) {
	case *svix.Error:
		msg := fmt.Sprintf("failed performing operation to Svix API, op=%v, resource=%v, status=%v, error=%v, body=%v",
			op, resourceName, e.Status(), e.Error(), string(e.Body()))
		log.Warn(msg)
		c.JSON(http.StatusBadRequest, gin.H{"message": msg})
		return true
	case nil:
	default:
		msg := fmt.Sprintf("failed performing operation to Svix API, op=%v, resource=%v, err=%v", op, resourceName, err)
		log.Warn(msg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": msg})
		return true
	}
	return false
}
