package controller

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hoophq/hoop/agent/controller/featureflagstate"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
)

// TestHttpProxyHostPortFlag covers DEP-258: experimental.httpproxy_host_port, as
// the gateway sends it, decides if the upstream Host keeps a non-default port.
func TestHttpProxyHostPortFlag(t *testing.T) {
	hosts := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts <- r.Host
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	remoteURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	prevState, err := json.Marshal(featureflagstate.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { featureflagstate.Update(map[string][]byte{pb.SpecFeatureFlagsKey: prevState}) })

	for _, tt := range []struct {
		name   string
		enable bool // false: the org never sets the flag, so the catalog default applies
		want   string
	}{
		{name: "org without the flag keeps the legacy Host", enable: false, want: remoteURL.Hostname()},
		{name: "flag on keeps the non-default port", enable: true, want: remoteURL.Host},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Same path as sendFeatureFlagSeed: catalog snapshot to agent state.
			orgID := fmt.Sprintf("dep258-%t", tt.enable)
			if tt.enable {
				featureflag.Set(orgID, httpProxyHostPortFlag, true)
				t.Cleanup(func() { featureflag.SetAll(orgID, map[string]bool{}) })
			}
			snapshot, err := json.Marshal(featureflag.SnapshotForOrg(orgID))
			if err != nil {
				t.Fatal(err)
			}
			featureflagstate.Update(map[string][]byte{pb.SpecFeatureFlagsKey: snapshot})

			sid := orgID
			a := &Agent{client: &loopTransport{}, connStore: memory.New()}
			a.connStore.Set(sid, &pb.AgentConnectionParams{
				ConnectionType: pb.ConnectionTypeHttpProxy.String(),
				EnvVars: map[string]any{
					"envvar:REMOTE_URL": base64.StdEncoding.EncodeToString([]byte(upstream.URL)),
				},
			})
			t.Cleanup(func() {
				if proxy, ok := a.connStore.Get(sid + ":1").(io.Closer); ok {
					_ = proxy.Close()
				}
			})
			a.handleHttpProxyWrite(&pb.Packet{
				Spec: map[string][]byte{
					pb.SpecGatewaySessionID:   []byte(sid),
					pb.SpecClientConnectionID: []byte("1"),
					pb.SpecHttpProxyBaseUrl:   []byte("http://127.0.0.1:8081"),
				},
				Payload: []byte("GET / HTTP/1.1\r\nHost: 127.0.0.1:8081\r\n\r\n"),
			})

			select {
			case got := <-hosts:
				if got != tt.want {
					t.Errorf("upstream Host = %q, want %q", got, tt.want)
				}
			default:
				t.Fatal("upstream got no request")
			}
		})
	}
}
