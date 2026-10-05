package daemon

import (
	"context"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// A return-mode denial names its review on the verdict, so a transport with
// structured fields carries it without parsing the message.
func TestAReturnDenyNamesItsReviewOnTheVerdict(t *testing.T) {
	pol, _ := returnMCPPolicy(t, false)
	v := pol.Evaluate(inspect.Statement{
		Protocol: inspect.Postgres, Direction: inspect.FromClient,
		Text: "DELETE FROM users", Operation: inspect.OpDelete, Tables: []string{"users"},
	})
	if !v.Denied || v.Review == nil {
		t.Fatalf("denied=%v review=%v, want a denial naming its review", v.Denied, v.Review)
	}
	if v.Review.ID != "9f97" || v.Review.Status != "PENDING" || !v.Review.Return {
		t.Errorf("review = %+v, want 9f97 PENDING in return mode", *v.Review)
	}
}

// gRPC: through a real lane, the review rides in the trailers beside
// grpc-status, under the same names an http lane uses for headers.
func TestAGRPCReviewDenyCarriesTheReviewTrailers(t *testing.T) {
	pol, _ := returnMCPPolicy(t, true)
	upstream, _ := firstReadUpstream(t)
	laneAddr, stop := startGRPCTestServer(t,
		buildHoldTestServer(t, "grpc", upstream, writeGRPCTestDescriptors(t), pol))
	defer stop()

	resp, err := holdTestRoundTrip(context.Background(), laneAddr, "/test.v1.Echo/Say",
		marshalGRPCTestMessage("s", "v"))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if got := spannerTestStatus(resp); got != "7" {
		t.Fatalf("grpc-status = %q, want 7", got)
	}
	for k, want := range map[string]string{
		"X-Hoop-Denied":        "review",
		"X-Hoop-Review-Id":     "9f97",
		"X-Hoop-Review-Status": "PENDING",
		"Retry-After":          "5",
	} {
		got := resp.Trailer.Get(k)
		if got == "" {
			got = resp.Header.Get(k) // a trailers-only response
		}
		if got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}
