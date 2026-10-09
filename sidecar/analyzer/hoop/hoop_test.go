package hoop_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/analyzer/hoop"
	"github.com/hoophq/libhoop/hostedllm"
)

// An outage of the hosted model must read as a hoop failure, not an openai
// one: the operator has no openai block to go and check.
func TestFailuresNameTheHoopProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(hostedllm.HeaderSignature) == "" {
			t.Error("request reached the endpoint unsigned")
		}
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p, err := analyzer.NewProvider(hoop.Name, analyzer.Options{Model: "m", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != hoop.Name {
		t.Errorf("Name() = %q, want %q", p.Name(), hoop.Name)
	}
	_, err = p.Classify(context.Background(), "s", "c")
	var he *analyzer.HTTPError
	if !errors.As(err, &he) || he.Provider != "analyzer/"+hoop.Name {
		t.Fatalf("Classify error = %v, want an HTTPError from analyzer/%s", err, hoop.Name)
	}
}
