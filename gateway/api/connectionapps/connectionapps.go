package apiconnectionapps

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/storagev2"
	connectionstorage "github.com/runopsio/hoop/gateway/storagev2/connection"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type ConnectionAppsRequest struct {
	ConnectionName string `json:"connection"`
}

func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	// connection attribute is unused here
	var reqBody ConnectionAppsRequest
	if err := c.ShouldBindJSON(&reqBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	reqBody.ConnectionName = strings.TrimSpace(strings.ToLower(reqBody.ConnectionName))
	dsnCtx := ctx.DSN()
	if dsnCtx.OrgID == "" || dsnCtx.ClientKeyName == "" || reqBody.ConnectionName == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "missing required attributes"})
		return
	}

	connectionName := fmt.Sprintf("%s:%s", dsnCtx.ClientKeyName, reqBody.ConnectionName)
	conn, err := connectionstorage.GetOneByName(ctx, connectionName)
	if err != nil {
		log.Errorf("failed validating connection, reason=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed validating connection"})
		return
	}
	if conn == nil {
		// TODO: add audit, dlp & indexer
		err := connectionstorage.Put(ctx, &types.Connection{
			Id:      uuid.NewString(),
			OrgId:   dsnCtx.OrgID,
			Name:    connectionName,
			Command: []string{"/bin/bash"},
			Type:    pb.ConnectionTypeCommandLine,
			AgentId: connectionName,
		})
		if err != nil {
			log.Errorf("failed creating connection, err=%v", err)
			sentry.CaptureException(err)
		}
	}
	if obj := Store.Get(connectionName); obj != nil {
		log.Infof("found a connection request for %v", connectionName)
		Store.Del(connectionName)
		c.PureJSON(200, gin.H{"grpc_url": ctx.GrpcURL})
		return
	}
	c.Status(http.StatusNoContent)
}
