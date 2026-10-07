package apihealthz

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/gateway/api/openapi"
)

// LivenessHandler
//
//	@Summary		HealthCheck
//	@Description	Reports if the service is working properly. The gRPC transport is checked only while experimental.agents is enabled for the organization
//	@Tags			Server Management
//	@Produce		json
//	@Success		200	{object}	openapi.LivenessCheck
//	@Failure		400	{object}	openapi.LivenessCheck
//	@Router			/healthz [get]
func LivenessHandler(defaultOrgID string) func(_ *gin.Context) {
	return func(c *gin.Context) {
		if probeGRPC(defaultOrgID) {
			if err := checkAddrLiveness(grpc.LocalhostAddr); err != nil {
				c.JSON(http.StatusBadRequest, openapi.LivenessCheck{Liveness: "ERR"})
				return
			}
		}
		c.JSON(http.StatusOK, openapi.LivenessCheck{Liveness: "OK"})
	}
}

// probeGRPC reports whether liveness includes the gRPC transport: the
// default org has experimental.agents on, or there is no default org
// (multi-tenant), where agents are always served.
func probeGRPC(defaultOrgID string) bool {
	return defaultOrgID == "" || featureflag.IsEnabled(defaultOrgID, featureflag.FlagAgents)
}

func checkAddrLiveness(addr string) error {
	timeout := time.Second * 3
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return fmt.Errorf("not responding, err=%v", err)
	}
	_ = conn.Close()
	return nil
}
