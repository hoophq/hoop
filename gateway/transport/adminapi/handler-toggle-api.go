package adminapi

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/storagev2"
	orgstorage "github.com/runopsio/hoop/gateway/storagev2/org"
	"github.com/runopsio/hoop/gateway/transport"
	"github.com/runopsio/hoop/gateway/transportv2/memorystreams"
)

func toggleApiV2(c *gin.Context) {
	orgID := c.Param("orgid")
	if _, err := uuid.Parse(orgID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": gin.H{"message": "organization id is not a valid uuid"}})
		return
	}
	log.With("org", orgID).Infof("promoting organization to api v2")

	store := storagev2.NewStorage(nil)
	if err := orgstorage.ToggleApiV2(store, orgID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": gin.H{
			"message": "failed updating organization to api v2",
			"reason":  err.Error(),
		}})
		return
	}
	restartedAgents := transport.DisconnectAllAgentsByOrg(orgID, fmt.Errorf("promoting agents to api v2"))
	msg := fmt.Sprintf("organization promoted with success to api v2. %v agent(s) restarted",
		restartedAgents)
	log.With("org", orgID).Info(msg)
	c.JSON(http.StatusOK, gin.H{"message": msg})
}

func toggleLegacyApi(c *gin.Context) {
	orgID := c.Param("orgid")
	if _, err := uuid.Parse(orgID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": gin.H{"message": "organization id is not a valid uuid"}})
		return
	}
	log.With("org", orgID).Infof("demoting organization to legacy api")
	store := storagev2.NewStorage(nil)
	if err := orgstorage.ToggleLegacyApi(store, orgID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": gin.H{
			"message": "failed updating organization to legacy api",
			"reason":  err.Error(),
		}})
		return
	}

	restartedAgents := memorystreams.DisconnectAllAgentsByOrg(orgID, fmt.Errorf("promoting agents to api v2"))
	msg := fmt.Sprintf("organization demoted with success to legacy api. %v agent(s) restarted",
		restartedAgents)
	log.With("org", orgID).Info(msg)
	c.JSON(http.StatusOK, gin.H{"message": msg})
}
