package apiproxymanager

import (
	"fmt"
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/log"
	pbclient "github.com/runopsio/hoop/common/proto/client"
	pgproxymanager "github.com/runopsio/hoop/gateway/pgrest/proxymanager"
	"github.com/runopsio/hoop/gateway/storagev2/clientstate"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/transport"
	"github.com/runopsio/hoop/gateway/user"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ProxyManagerRequest struct {
	ConnectionName string        `json:"connection_name"`
	Port           string        `json:"port"`
	AccessDuration time.Duration `json:"access_duration"`
}

type ProxyManagerResponse struct {
	ID                    string                 `json:"id"`
	Status                types.ClientStatusType `json:"status"`
	RequestConnectionName string                 `json:"connection_name"`
	RequestPort           string                 `json:"port"`
	RequestAccessDuration time.Duration          `json:"access_duration"`
	ClientMetadata        map[string]string      `json:"metadata"`
	ConnectedAt           string                 `json:"connected-at"`
}

// func getEntity(ctx *storagev2.Context) (*types.Client, error) {
// 	obj, err := clientstate.GetEntity(ctx, clientstate.DeterministicClientUUID(ctx.UserID))
// 	if err != nil {
// 		return nil, fmt.Errorf("failed obtaining client state resource, err=%v", err)
// 	}
// 	return obj, nil
// }

func Get(c *gin.Context) {
	ctx := user.ContextUser(c)
	obj, err := pgproxymanager.New().FetchOne(ctx, clientstate.DeterministicClientUUID(ctx.GetUserID()))
	// obj, err := getEntity(ctx)
	if err != nil {
		log.Error(err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed obtaining client entity"})
		return
	}
	if obj == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "entity not found"})
		return
	}
	c.JSON(http.StatusOK, &ProxyManagerResponse{
		ID:                    obj.ID,
		Status:                obj.Status,
		RequestConnectionName: obj.RequestConnectionName,
		RequestPort:           obj.RequestPort,
		RequestAccessDuration: obj.RequestAccessDuration,
		ClientMetadata:        obj.ClientMetadata,
		ConnectedAt:           obj.ConnectedAt.Format(time.RFC3339),
	})
}

func Post(c *gin.Context) {
	ctx := user.ContextUser(c)
	var req ProxyManagerRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.ConnectionName == "" || req.Port == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": `port and connection_name are required attributes`})
		return
	}

	if req.AccessDuration == 0 {
		req.AccessDuration = time.Minute * 30
	}

	hasSubscribed := false
	for i := 1; i <= 10; i++ {
		log.Debugf("attempt=%v - dispatching open session", i)
		pkt, err := transport.DispatchOpenSession(&types.Client{
			ID:                    clientstate.DeterministicClientUUID(ctx.GetUserID()),
			RequestConnectionName: req.ConnectionName,
			RequestPort:           req.Port,
			RequestAccessDuration: req.AccessDuration,
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
			obj, err := clientstate.Update(ctx, types.ClientStatusDisconnected)
			if err != nil {
				errMsg := fmt.Sprintf("failed updating status, err=%v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
				return
			}
			// disconnect grpc-client
			_ = transport.DispatchDisconnect(obj)
			c.Header("Location", string(pkt.Payload))
			c.JSON(http.StatusOK, &ProxyManagerResponse{
				ID:                    obj.ID,
				Status:                obj.Status,
				RequestConnectionName: obj.RequestConnectionName,
				RequestPort:           obj.RequestPort,
				RequestAccessDuration: obj.RequestAccessDuration,
				ClientMetadata:        obj.ClientMetadata,
				ConnectedAt:           obj.ConnectedAt.Format(time.RFC3339),
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
	obj, err := clientstate.Update(ctx, types.ClientStatusConnected,
		clientstate.WithRequestAttributes(req.ConnectionName, req.Port, req.AccessDuration.String())...)
	if err != nil {
		log.Errorf("fail to update status, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "connected but it fail to update the status"})
		return
	}

	c.JSON(http.StatusOK, &ProxyManagerResponse{
		ID:                    obj.ID,
		Status:                obj.Status,
		RequestConnectionName: obj.RequestConnectionName,
		RequestPort:           obj.RequestPort,
		RequestAccessDuration: obj.RequestAccessDuration,
		ClientMetadata:        obj.ClientMetadata,
		ConnectedAt:           obj.ConnectedAt.Format(time.RFC3339),
	})
}

func Disconnect(c *gin.Context) {
	ctx := user.ContextUser(c)
	obj, err := pgproxymanager.New().FetchOne(ctx, clientstate.DeterministicClientUUID(ctx.GetUserID()))
	if err != nil {
		log.Error(err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed obtaining client entity"})
		return
	}
	if obj == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "entity not found"})
		return
	}
	if err := transport.DispatchDisconnect(obj); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	obj, err = clientstate.Update(ctx, types.ClientStatusDisconnected)
	if err != nil {
		log.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "disconnected grpc client, but it fail to update the status"})
		return
	}
	c.JSON(http.StatusAccepted, &ProxyManagerResponse{
		ID:                    obj.ID,
		Status:                obj.Status,
		RequestConnectionName: obj.RequestConnectionName,
		RequestPort:           obj.RequestPort,
		RequestAccessDuration: obj.RequestAccessDuration,
		ClientMetadata:        obj.ClientMetadata,
		ConnectedAt:           obj.ConnectedAt.Format(time.RFC3339),
	})
}
