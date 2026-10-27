package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/session"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

// grpcTestRefusingAuth is a pooled lane's verifier that admits nobody. The
// methods a refused RPC never reaches stay on the nil embedded interface.
type grpcTestRefusingAuth struct{ LaneAuth }

func (grpcTestRefusingAuth) Authenticate(_ context.Context, user, _ string) (session.Identity, Credential, error) {
	return session.Identity{Subject: user}, Credential{}, fmt.Errorf("%w: no key for %q", codectypes.ErrLoginRefused, user)
}

// A refused login on a pooled grpc lane is recorded under the user the
// login named, or under nobody: the identity header is a claim the lane
// never verified, and the login is the lane's only verifier.
func TestAGRPCRefusedLoginIsNotRecordedUnderTheIdentityHeader(t *testing.T) {
	upstreamAddr, stopUpstream := startGRPCTestH2C(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer stopUpstream()

	for _, tc := range []struct {
		name          string
		authorization string
		wantPrincipal string
	}{
		{"no authorization", "", "anonymous"},
		{"bearer without a user", "Bearer hlc_key", "anonymous"},
		{"basic names the user", "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:hlc_key")), "alice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := newGRPCTestMemorySink()
			server, _, err := buildGRPCServer(lane{
				cfg: ListenerConfig{
					Name:           "pooled",
					Protocol:       "grpc",
					Listen:         "127.0.0.1:0",
					Upstream:       upstreamAddr,
					IdentityHeader: "x-user",
				},
				name: "pooled",
				auth: grpcTestRefusingAuth{},
			}, AuditConfig{}, sink, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			laneAddr, stopLane := startGRPCTestServer(t, server)
			defer stopLane()

			transport := grpcTestTransport()
			defer transport.CloseIdleConnections()
			req, err := http.NewRequest(http.MethodPost, "http://"+laneAddr+"/test.v1.Echo/Say",
				bytes.NewReader(grpcTestFrame(0, nil)))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/grpc")
			req.Header.Set("X-User", "spoofed")
			if tc.authorization != "" {
				req.Header.Set("Authorization", tc.authorization)
			}
			resp, err := transport.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			select {
			case <-sink.ended:
			case <-time.After(time.Second):
				t.Fatal("session end was not audited")
			}
			events := sink.snapshot()
			var refused bool
			for _, e := range events {
				if e.Principal != tc.wantPrincipal {
					t.Errorf("%s principal = %q, want %q", e.Kind, e.Principal, tc.wantPrincipal)
				}
				if e.Kind == audit.KindViolation && e.Rule == "identity" {
					refused = true
				}
			}
			if !refused {
				t.Fatalf("no identity violation recorded: %+v", events)
			}
		})
	}
}
