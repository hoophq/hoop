//go:build integration

package transport

import (
	"context"
	"testing"
	"time"

	pbagent "github.com/hoophq/hoop/common/proto/agent"
)

// TestAgentReconnect verifies the transport's reconnect behavior end to end:
// an agent connects (connection goes online), the stream drops (connection
// goes offline), and a fresh Connect brings it back online. This is the state
// machine the agent's exponential-backoff reconnect loop drives in production,
// and it must behave identically on any wire.
func TestAgentReconnect(t *testing.T) {
	for _, c := range transports() {
		t.Run(c.Name(), func(t *testing.T) {
			connName := uniqueName("conn")
			agentID, dsn := createAgent(t, uniqueName("agent"))
			createPGConnection(t, connName, agentID)

			// First connection.
			first, err := c.DialAgent(context.Background(), dsn)
			if err != nil {
				t.Fatalf("DialAgent(first): %v", err)
			}
			if _, err := recvUntil(first, 10*time.Second, pbagent.GatewayConnectOK); err != nil {
				t.Fatalf("first handshake: %v", err)
			}
			waitConnectionStatus(t, connName, "online")

			// Drop the stream; the gateway must observe the disconnect and mark
			// the connection offline.
			first.Close()
			waitConnectionStatus(t, connName, "offline")

			// Reconnect with the same identity; the connection recovers.
			second, err := c.DialAgent(context.Background(), dsn)
			if err != nil {
				t.Fatalf("DialAgent(second): %v", err)
			}
			defer second.Close()
			if _, err := recvUntil(second, 10*time.Second, pbagent.GatewayConnectOK); err != nil {
				t.Fatalf("second handshake: %v", err)
			}
			waitConnectionStatus(t, connName, "online")
		})
	}
}
