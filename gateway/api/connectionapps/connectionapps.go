package apiconnectionapps

import (
	"fmt"
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/storagev2"
	connectionstorage "github.com/runopsio/hoop/gateway/storagev2/connection"
	pluginstorage "github.com/runopsio/hoop/gateway/storagev2/plugin"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type ConnectionAppsRequest struct {
	// DEPRECATED in flavor of connection_items attribute
	ConnectionName  string   `json:"connection"`
	ConnectionItems []string `json:"connection_items"`
}

func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var reqBody ConnectionAppsRequest
	if err := c.ShouldBindJSON(&reqBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	// maintain compatibility with old agents
	if len(reqBody.ConnectionItems) == 0 && reqBody.ConnectionName != "" {
		reqBody.ConnectionItems = append(reqBody.ConnectionItems, reqBody.ConnectionName)
	}
	dsnCtx := ctx.DSN()
	log := log.With("org", dsnCtx.OrgID, "key", dsnCtx.ClientKeyName)
	if dsnCtx.OrgID == "" || dsnCtx.ClientKeyName == "" || len(reqBody.ConnectionItems) == 0 {
		log.With("connections", len(reqBody.ConnectionItems)).Warnf("missing required attributes")
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "missing required attributes"})
		return
	}
	var requestConnectionItems []string
	for _, connectionName := range reqBody.ConnectionItems {
		connectionName := fmt.Sprintf("%s:%s", dsnCtx.ClientKeyName, connectionName)
		requestConnectionItems = append(requestConnectionItems, connectionName)
	}

	connectionMap, err := connectionstorage.ListConnectionsByList(ctx, requestConnectionItems)
	if err != nil {
		log.Errorf("failed validating connection, reason=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed validating connection"})
		return
	}

	// persist the requested connections if it doesn't exist in the storage yet
	for _, connectionName := range requestConnectionItems {
		if _, ok := connectionMap[connectionName]; ok {
			continue
		}
		log.Infof("publishing connection %v", connectionName)
		connID := uuid.NewString()
		err := connectionstorage.Put(ctx, &types.Connection{
			Id:      connID,
			OrgId:   dsnCtx.OrgID,
			Name:    connectionName,
			Command: []string{"/bin/bash"},
			Type:    pb.ConnectionTypeCommandLine,
			AgentId: connectionName,
		})
		if err != nil {
			// best effort, move on
			log.Errorf("failed creating connection, err=%v", err)
			sentry.CaptureException(err)
		} else {
			pluginstorage.EnableDefaultPlugins(ctx, connID, connectionName)
		}
	}
	// check if there's a client connection request
	// based on the published connections by the agent
	for _, connectionName := range requestConnectionItems {
		requestOK := requestGrpcConnectionOK(connectionName)
		if requestOK {
			c.PureJSON(200, gin.H{"grpc_url": ctx.GrpcURL})
			return
		}
	}
	c.Status(http.StatusNoContent)
}
