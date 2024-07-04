package apihealthz

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/gateway/pgrest"
)

// Liveness validates if the gateway ports has connectivity
func LivenessHandler() func(_ *gin.Context) {
	return func(c *gin.Context) {
		grpcLivenessErr := checkAddrLiveness(grpc.LocalhostAddr)
		apiLivenessErr := pgrest.CheckLiveness()
		if grpcLivenessErr != nil || apiLivenessErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"liveness": "ERR"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"liveness": "OK"})
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
