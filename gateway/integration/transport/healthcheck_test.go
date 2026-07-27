//go:build integration

package transport

import (
	"context"
	"testing"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
)

// TestHealthCheck verifies the unary HealthCheck RPC answers on every wire.
// It is the simplest parity anchor: the WebSocket transport must return the
// same "OK" status through the same interceptor stack.
func TestHealthCheck(t *testing.T) {
	for _, c := range transports() {
		t.Run(c.Name(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			resp, err := c.HealthCheck(ctx, &pb.HealthCheckRequest{})
			if err != nil {
				t.Fatalf("HealthCheck: %v", err)
			}
			if resp.Status != "OK" {
				t.Fatalf("HealthCheck: status = %q, want %q", resp.Status, "OK")
			}
		})
	}
}
