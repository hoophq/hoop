package apihealthz

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/pgrest"
)

// LivenessHandler
//
//	@Summary		HealthCheck
//	@Description	Reports if the service is working properly
//	@Tags			Server Management
//	@Produce		json
//	@Success		200	{object}	openapi.LivenessCheck
//	@Failure		400	{object}	openapi.LivenessCheck
//	@Router			/healthz [get]
func LivenessHandler() func(_ *gin.Context) {
	return func(c *gin.Context) {
		grpcLivenessErr := checkAddrLiveness(grpc.LocalhostAddr)
		apiLivenessErr := pgrest.CheckLiveness()
		if grpcLivenessErr != nil || apiLivenessErr != nil {
			c.JSON(http.StatusBadRequest, openapi.LivenessCheck{Liveness: "ERR"})
			return
		}
		c.JSON(http.StatusOK, openapi.LivenessCheck{Liveness: "OK"})
	}
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
