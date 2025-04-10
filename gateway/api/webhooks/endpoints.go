package webhooks

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/storagev2"
	svix "github.com/svix/svix-webhooks/go"
)

func CreateSvixEndpoint(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	svixClient, err := getSvixClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	var req svix.EndpointIn
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	response, err := svixClient.Endpoint.Create(context.TODO(), ctx.GetOrgID(), &req)
	if isApiError(c, "endpoint-create", "", err) {
		return
	}

	c.JSON(http.StatusCreated, response)
}

func GetSvixEndpointByID(c *gin.Context) {
	ctx, endpointID := storagev2.ParseContext(c), c.Param("id")
	svixClient, err := getSvixClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	response, err := svixClient.Endpoint.Get(context.Background(), ctx.GetOrgID(), endpointID)
	if isApiError(c, "endpoint-get", endpointID, err) {
		return
	}
	c.JSON(http.StatusOK, response)
}

// from svix.ListResponseEndpointOut
type ListResponseEndpointOut struct {
	Data         []EndpointOut `json:"data"`
	Done         bool          `json:"done"`
	Iterator     *string       `json:"iterator,omitempty"`
	PrevIterator *string       `json:"prevIterator,omitempty"`
}

// from svix.EndpointOut
type EndpointOut struct {
	Channels    []string            `json:"channels,omitempty"`
	CreatedAt   time.Time           `json:"createdAt"`
	Description string              `json:"description"`
	Disabled    *bool               `json:"disabled,omitempty"`
	FilterTypes []string            `json:"filterTypes,omitempty"`
	Id          string              `json:"id"`
	Metadata    map[string]string   `json:"metadata"`
	RateLimit   *int32              `json:"rateLimit,omitempty"`
	Uid         *string             `json:"uid,omitempty"`
	UpdatedAt   time.Time           `json:"updatedAt"`
	Url         string              `json:"url"`
	Version     int32               `json:"version"`
	Stats       *svix.EndpointStats `json:"stats"`
}

func ListSvixEndpoints(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	svixClient, err := getSvixClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	appID := ctx.GetOrgID()

	response, err := svixClient.Endpoint.List(context.Background(), appID, &svix.EndpointListOptions{})
	if isApiError(c, "endpoint-list", "", err) {
		return
	}

	newResponse := ListResponseEndpointOut{
		Done:         response.Done,
		Iterator:     response.Iterator.Get(),
		PrevIterator: response.PrevIterator.Get(),
		Data:         []EndpointOut{},
	}

	for _, v := range response.Data {
		stats, err := svixClient.Endpoint.GetStatsWithOptions(context.Background(), appID, v.Id, svix.EndpointStatsOptions{})
		if err != nil {
			stats = &svix.EndpointStats{Fail: -1, Success: -1, Sending: -1, Pending: -1}
		}
		newResponse.Data = append(newResponse.Data, EndpointOut{
			Channels:    v.Channels,
			CreatedAt:   v.CreatedAt,
			Description: v.Description,
			Disabled:    v.Disabled,
			FilterTypes: v.FilterTypes,
			Id:          v.Id,
			Metadata:    v.Metadata,
			RateLimit:   v.RateLimit.Get(),
			Uid:         v.Uid.Get(),
			UpdatedAt:   v.UpdatedAt,
			Url:         v.Url,
			Version:     v.Version,
			Stats:       stats,
		})
	}
	c.JSON(http.StatusOK, newResponse)
}

func UpdateSvixEndpoint(c *gin.Context) {
	ctx, endpointID := storagev2.ParseContext(c), c.Param("id")
	svixClient, err := getSvixClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	var req svix.EndpointUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	response, err := svixClient.Endpoint.Update(context.TODO(), ctx.GetOrgID(), endpointID, &req)
	if isApiError(c, "endpoint-update", endpointID, err) {
		return
	}
	c.JSON(http.StatusOK, response)

}

func DeleteSvixEndpointByID(c *gin.Context) {
	ctx, endpointID := storagev2.ParseContext(c), c.Param("id")
	svixClient, err := getSvixClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	err = svixClient.Endpoint.Delete(context.Background(), ctx.GetOrgID(), endpointID)
	if isApiError(c, "endpoint-delete", endpointID, err) {
		return
	}
	c.JSON(http.StatusNoContent, nil)
}
