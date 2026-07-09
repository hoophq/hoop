//go:build integration

package transport

import (
	"context"
	"testing"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
)

// TestPreConnectBackoff verifies that an agent with no pending client requests
// is told to back off. This is the steady-state answer the agent's reconnect
// loop polls against.
func TestPreConnectBackoff(t *testing.T) {
	for _, c := range transports() {
		t.Run(c.Name(), func(t *testing.T) {
			_, dsn := createAgent(t, uniqueName("agent"))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			resp, err := c.PreConnect(ctx, dsn, &pb.PreConnectRequest{})
			if err != nil {
				t.Fatalf("PreConnect: %v", err)
			}
			if resp.Status != pb.PreConnectStatusBackoffType {
				t.Fatalf("PreConnect status = %q, want %q", resp.Status, pb.PreConnectStatusBackoffType)
			}
		})
	}
}

// TestPreConnectConnectWhenClientWaiting verifies the coordination contract:
// when a client is blocked waiting for an offline agent's connection, the
// agent's next PreConnect returns CONNECT so it knows to open a Connect stream.
// This exercises PreConnect together with the connection-request store that
// links client dials to agent wake-ups.
func TestPreConnectConnectWhenClientWaiting(t *testing.T) {
	for _, c := range transports() {
		t.Run(c.Name(), func(t *testing.T) {
			connName := uniqueName("conn")
			agentID, dsn := createAgent(t, uniqueName("agent"))
			createPGConnection(t, connName, agentID)

			// Dial as a client while the agent is offline; the gateway parks the
			// request waiting for the agent, which is what flips PreConnect to
			// CONNECT. Keep the stream open for the duration of the test.
			clientCtx, cancelClient := context.WithCancel(context.Background())
			defer cancelClient()
			dialed := make(chan error, 1)
			go func() {
				cli, err := c.DialClient(context.Background(), ClientDialConfig{
					Token:          adminToken(t),
					ConnectionName: connName,
					Verb:           pb.ClientVerbConnect,
				})
				dialed <- err
				if err != nil {
					return
				}
				defer cli.Close()
				// Hold the stream open until the test finishes; drain packets so
				// the parked request stays registered.
				for {
					if _, err := cli.Recv(); err != nil {
						return
					}
					select {
					case <-clientCtx.Done():
						return
					default:
					}
				}
			}()

			// Fail with a precise diagnostic if the client could not even dial,
			// rather than burning the whole timeout and reporting "never CONNECT".
			select {
			case err := <-dialed:
				if err != nil {
					t.Fatalf("client dial: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("client dial did not complete")
			}

			// The parked request registers shortly after the dial; poll
			// PreConnect until it observes it.
			deadline := time.Now().Add(12 * time.Second)
			for time.Now().Before(deadline) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				resp, err := c.PreConnect(ctx, dsn, &pb.PreConnectRequest{})
				cancel()
				if err != nil {
					t.Fatalf("PreConnect: %v", err)
				}
				if resp.Status == pb.PreConnectStatusConnectType {
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Fatal("PreConnect never returned CONNECT while a client was waiting")
		})
	}
}
