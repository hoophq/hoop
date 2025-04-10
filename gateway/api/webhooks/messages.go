package webhooks

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/storagev2"
	svix "github.com/svix/svix-webhooks/go"
)

func CreateSvixMessage(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	svixClient, err := getSvixClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	var req svix.MessageIn
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	response, err := svixClient.Message.Create(context.TODO(), ctx.GetOrgID(), &req)
	if isApiError(c, "message-create", "", err) {
		return
	}
	c.JSON(http.StatusCreated, response)
}

func ListSvixMessages(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	svixClient, err := getSvixClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	appID := ctx.GetOrgID()
	endpoints, err := svixClient.Endpoint.List(context.Background(), appID, &svix.EndpointListOptions{})
	if isApiError(c, "message-list", "", err) {
		return
	}
	endpointID := c.Query("endpoint_id")
	if len(endpoints.Data) == 1 && endpointID == "" {
		endpointID = endpoints.Data[0].Id
	}
	if endpointID == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "a default endpoint was not found, specify one by passing the query string: endpoint_id"})
		return
	}
	var eventTypes *[]string
	if eventTypeQuery := c.Query("event_types"); eventTypeQuery != "" {
		etqs := strings.Split(eventTypeQuery, ",")
		eventTypes = &etqs
	}

	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit == 0 {
		limit = 50
	}

	response, err := svixClient.MessageAttempt.ListByEndpoint(
		context.Background(),
		appID,
		endpointID,
		&svix.MessageAttemptListOptions{EventTypes: eventTypes, Limit: ptr.Int32(int32(limit))})
	if isApiError(c, "message-list", "", err) {
		return
	}
	c.JSON(http.StatusOK, response)
}

func GetSvixMessageByID(c *gin.Context) {
	ctx, messageID := storagev2.ParseContext(c), c.Param("id")
	svixClient, err := getSvixClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	response, err := svixClient.Message.Get(context.Background(), ctx.GetOrgID(), messageID)
	if isApiError(c, "message-get", messageID, err) {
		return
	}
	c.JSON(http.StatusOK, response)
}
