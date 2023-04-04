package healthz

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/grpc"
)

// Liveness validates if the gateway ports (8009-8010) has connectivity
func LivenessHandler(c *gin.Context) {
	grpcLivenessErr := checkAddrLiveness(grpc.LocalhostAddr)
	apiLivenessErr := checkAddrLiveness("127.0.0.1:8009")
	if grpcLivenessErr != nil || apiLivenessErr != nil {
		msg := fmt.Sprintf("gateway-grpc=%v, gateway-api=%v", grpcLivenessErr, apiLivenessErr)
		c.JSON(http.StatusBadRequest, gin.H{"liveness": msg})
		return
	}
	c.JSON(http.StatusOK, gin.H{"liveness": "OK"})
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
