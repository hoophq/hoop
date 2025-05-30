package apiproxymanager

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/clientstate"
	"github.com/hoophq/hoop/gateway/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ProxyManagerStatus
//
//	@Summary		ProxyManager Status
//	@Description	Get the current status of the client
//	@Tags			Proxy Manager
//	@Param			type	query	string	false	"Filter by type"	Format(string)
//	@Produce		json
//	@Success		200			{object}	openapi.ProxyManagerResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/proxymanager/status [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	obj, err := models.GetProxyManagerStateByID(ctx.OrgID, clientstate.DeterministicClientUUID(ctx.UserID))
	switch err {
	case nil:
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "entity not found"})
		return
	default:
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed obtaining client entity"})
		return
	}

	conn, err := models.GetConnectionByNameOrID(ctx, obj.RequestConnectionName)
	if err != nil {
		log.Errorf("failed retrieving connection %v, reason=%v", obj.RequestConnectionName, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error, failed to obtaining connection"})
		return
	}

	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
		return
	}

	c.JSON(http.StatusOK, &openapi.ProxyManagerResponse{
		ID:                       obj.ID,
		Status:                   openapi.ClientStatusType(obj.Status),
		RequestConnectionName:    obj.RequestConnectionName,
		RequestConnectionType:    conn.Type,
		RequestConnectionSubType: conn.SubType.String,
		RequestPort:              obj.RequestPort,
		RequestAccessDuration:    time.Duration(obj.RequestAccessDurationSec) * time.Second,
		ClientMetadata:           obj.ClientMetadata,
		ConnectedAt:              obj.ConnectedAt.Format(time.RFC3339),
	})
}

// ProxyManagerConnect
//
//	@Summary		ProxyManager Connect
//	@Description	Send a connect request to the client. A successful response indicates the client has stablished a connection.
//	@Description	If the connection resource has the review enabled, it returns a successful response containing the link of the review in the `Localtion` header.
//	@Tags			Proxy Manager
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.ProxyManagerRequest	true	"The request body resource"
//	@Success		200				{object}	openapi.ProxyManagerResponse
//	@Header			200				{string}	Location	"It will contain the url of the review in case the connection resource has the review enabled"
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/proxymanager/connect [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.ProxyManagerRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.ConnectionName == "" || req.Port == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": `port and connection_name are required attributes`})
		return
	}

	conn, err := models.GetConnectionByNameOrID(ctx, req.ConnectionName)
	if err != nil {
		log.Errorf("failed retrieving connection %v, reason=%v", req.ConnectionName, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal error, failed to obtaining connection"})
		return
	}

	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
		return
	}

	if req.AccessDuration == 0 {
		req.AccessDuration = time.Minute * 30
	}

	hasSubscribed := false
	for i := 1; i <= 10; i++ {
		log.Debugf("attempt=%v - dispatching open session", i)
		pkt, err := transport.DispatchOpenSession(&models.ProxyManagerState{
			ID:                       clientstate.DeterministicClientUUID(ctx.UserID),
			RequestConnectionName:    req.ConnectionName,
			RequestPort:              req.Port,
			RequestAccessDurationSec: int(req.AccessDuration.Seconds()),
		})
		if status, ok := status.FromError(err); ok {
			switch status.Code() {
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
				return
			}
		}
		if err == transport.ErrForceReconnect {
			continue
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
			return
		}
		if pkt == nil {
			hasSubscribed = true
			break
		}

		switch pkt.Type {
		case pbclient.SessionOpenWaitingApproval:
			obj, err := clientstate.Update(ctx, models.ProxyManagerStatusDisconnected)
			if err != nil {
				errMsg := fmt.Sprintf("failed updating status, err=%v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
				return
			}
			// disconnect grpc-client
			_ = transport.DispatchDisconnect(obj)
			c.Header("Location", string(pkt.Payload))
			c.JSON(http.StatusOK, &openapi.ProxyManagerResponse{
				ID:                       obj.ID,
				Status:                   openapi.ClientStatusType(obj.Status),
				RequestConnectionName:    obj.RequestConnectionName,
				RequestConnectionType:    conn.Type,
				RequestConnectionSubType: conn.SubType.String,
				RequestPort:              obj.RequestPort,
				HasReview:                true,
				RequestAccessDuration:    time.Duration(obj.RequestAccessDurationSec) * time.Second,
				ClientMetadata:           obj.ClientMetadata,
				ConnectedAt:              obj.ConnectedAt.Format(time.RFC3339),
			})
		default:
			errMsg := fmt.Sprintf("internal error, packet %v condition not implemented", pkt.Type)
			c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
		}
		return
	}
	if !hasSubscribed {
		c.JSON(http.StatusBadRequest, gin.H{"message": "max attempts (10) reached trying to subscribe"})
		return
	}
	obj, err := clientstate.Update(ctx, models.ProxyManagerStatusConnected,
		clientstate.WithRequestAttributes(req.ConnectionName, req.Port, req.AccessDuration.String())...)
	if err != nil {
		log.Errorf("fail to update status, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "connected but it fail to update the status"})
		return
	}

	c.JSON(http.StatusOK, &openapi.ProxyManagerResponse{
		ID:                       obj.ID,
		Status:                   openapi.ClientStatusType(obj.Status),
		RequestConnectionName:    obj.RequestConnectionName,
		RequestConnectionType:    conn.Type,
		RequestConnectionSubType: conn.SubType.String,
		RequestPort:              obj.RequestPort,
		RequestAccessDuration:    time.Duration(obj.RequestAccessDurationSec) * time.Second,
		ClientMetadata:           obj.ClientMetadata,
		ConnectedAt:              obj.ConnectedAt.Format(time.RFC3339),
	})
}

// ProxyManagerDisconnect
//
//	@Summary		ProxyManager Disconnect
//	@Description	Send a disconnect request. The transport layer will disconnect the connected client asynchronously
//	@Tags			Proxy Manager
//	@Produce		json
//	@Success		202			{object}	openapi.ProxyManagerResponse
//	@Failure		404,422,500	{object}	openapi.HTTPError
//	@Router			/proxymanager/disconnect [post]
func Disconnect(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	obj, err := models.GetProxyManagerStateByID(ctx.GetOrgID(), clientstate.DeterministicClientUUID(ctx.GetUserID()))
	switch err {
	case nil:
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "entity not found"})
		return
	default:
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed obtaining client entity"})
		return
	}

	if err := transport.DispatchDisconnect(obj); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	obj, err = clientstate.Update(ctx, models.ProxyManagerStatusDisconnected)
	if err != nil {
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "disconnected grpc client, but it fail to update the status"})
		return
	}
	c.JSON(http.StatusAccepted, &openapi.ProxyManagerResponse{
		ID:                    obj.ID,
		Status:                openapi.ClientStatusType(obj.Status),
		RequestConnectionName: obj.RequestConnectionName,
		RequestPort:           obj.RequestPort,
		RequestAccessDuration: time.Duration(obj.RequestAccessDurationSec) * time.Second,
		ClientMetadata:        obj.ClientMetadata,
		ConnectedAt:           obj.ConnectedAt.Format(time.RFC3339),
	})
}
