package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// A server-streaming RPC costs one OPA round trip per response message on a
// capturing lane, so a 50k-row read is 50k serial calls on the response
// path. Both switches keep OPA on the request side and the request side
// only, through the real lane: headers statement, request message, five
// response messages, trailer.
func TestGRPCLaneOPASkipsResponseMessages(t *testing.T) {
	const rows = 5
	descriptorPath := writeGRPCTestDescriptors(t)
	upstreamAddr, stopUpstream := startGRPCTestH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		w.WriteHeader(http.StatusOK)
		for i := range rows {
			_, _ = w.Write(grpcTestFrame(0, marshalGRPCTestMessage("row", string(rune('a'+i)))))
		}
		w.Header().Set("Grpc-Status", "0")
	}))
	defer stopUpstream()

	// countingOPA allows everything, counts calls by direction, and answers
	// a request with `responses: false` when optOut is set.
	countingOPA := func(t *testing.T, optOut bool) (string, *atomic.Int32, *atomic.Int32) {
		var requests, responses atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Input struct {
					Direction string `json:"direction"`
				} `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding OPA input: %v", err)
			}
			result := `{"result":{"allow":true}}`
			switch body.Input.Direction {
			case string(inspect.FromClient):
				requests.Add(1)
				if optOut {
					result = `{"result":{"allow":true,"responses":false}}`
				}
			case string(inspect.FromServer):
				responses.Add(1)
			}
			_, _ = w.Write([]byte(result))
		}))
		t.Cleanup(srv.Close)
		return srv.URL, &requests, &responses
	}

	run := func(t *testing.T, evaluator policy.Evaluator) {
		t.Helper()
		server := buildGRPCTestServer(t, "opa-responses", upstreamAddr,
			&GRPCCodecConfig{Descriptors: DescriptorPaths{descriptorPath}, CapturePayload: true},
			evaluator, nil, nil)
		laneAddr, stopLane := startGRPCTestServer(t, server)
		defer stopLane()

		transport := grpcTestTransport()
		defer transport.CloseIdleConnections()
		req, err := http.NewRequest(http.MethodPost, "http://"+laneAddr+"/test.v1.Echo/Say",
			bytes.NewReader(grpcTestFrame(0, marshalGRPCTestMessage("q", "request"))))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/grpc")
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if got := resp.Trailer.Get("Grpc-Status"); got != "0" {
			t.Fatalf("grpc-status = %q, want 0", got)
		}
		if n := bytes.Count(body, []byte("row")); n != rows {
			t.Fatalf("client saw %d rows, want %d; skipping OPA must not drop messages", n, rows)
		}
	}

	t.Run("lane switch", func(t *testing.T) {
		url, requests, responses := countingOPA(t, false)
		off := false
		run(t, policy.Chain{(&OPAConfig{URL: url, Responses: &off}).client("")})
		if requests.Load() != 2 {
			t.Errorf("request-side OPA calls = %d, want 2 (headers + message)", requests.Load())
		}
		if responses.Load() != 0 {
			t.Errorf("response-side OPA calls = %d under opa.responses: false", responses.Load())
		}
	})

	t.Run("policy opt-out", func(t *testing.T) {
		url, requests, responses := countingOPA(t, true)
		run(t, policy.Chain{(&OPAConfig{URL: url}).client("")})
		if requests.Load() != 2 {
			t.Errorf("request-side OPA calls = %d, want 2 (headers + message)", requests.Load())
		}
		if responses.Load() != 0 {
			t.Errorf("response-side OPA calls = %d after the policy returned responses: false", responses.Load())
		}
	})

	t.Run("default asks on every message", func(t *testing.T) {
		url, _, responses := countingOPA(t, false)
		run(t, policy.Chain{(&OPAConfig{URL: url}).client("")})
		if responses.Load() != rows+1 {
			t.Errorf("response-side OPA calls = %d, want %d (messages + trailer)", responses.Load(), rows+1)
		}
	})
}
