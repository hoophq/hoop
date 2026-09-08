package apiopaconfigs

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/api/openapi"
)

func TestValidateOPAConfigRequest(t *testing.T) {
	valid := func(mutate func(*openapi.OPAConfigRequest)) *openapi.OPAConfigRequest {
		req := &openapi.OPAConfigRequest{
			Name:       "prod-opa",
			URL:        "http://opa:8181/v1/data/hoop/inspect",
			TimeoutSec: 2,
		}
		if mutate != nil {
			mutate(req)
		}
		return req
	}

	for _, tt := range []struct {
		name    string
		req     *openapi.OPAConfigRequest
		wantErr string
	}{
		{"http endpoint", valid(nil), ""},
		{"https endpoint", valid(func(r *openapi.OPAConfigRequest) {
			r.URL = "https://opa.internal/v1/data/hoop/inspect"
		}), ""},
		{"host without path", valid(func(r *openapi.OPAConfigRequest) { r.URL = "http://opa:8181" }), ""},

		{"uuid name", valid(func(r *openapi.OPAConfigRequest) {
			r.Name = "1837453e-01fc-46f3-9e4c-dcf22d395393"
		}), "name: it must not be a uuid"},
		{"name too short", valid(func(r *openapi.OPAConfigRequest) { r.Name = "op" }), "name: it must contain"},

		{"relative url", valid(func(r *openapi.OPAConfigRequest) { r.URL = "/v1/data/hoop/inspect" }), "url: it must be an absolute"},
		{"scheme relative url", valid(func(r *openapi.OPAConfigRequest) { r.URL = "//opa:8181/v1/data" }), "url: it must be an absolute"},
		{"host only", valid(func(r *openapi.OPAConfigRequest) { r.URL = "opa:8181" }), "url: it must be an absolute"},
		{"unsupported scheme", valid(func(r *openapi.OPAConfigRequest) { r.URL = "grpc://opa:8181/v1/data" }), "url: it must be an absolute"},
		{"empty url", valid(func(r *openapi.OPAConfigRequest) { r.URL = "" }), "url: it must be an absolute"},
		{"unparsable url", valid(func(r *openapi.OPAConfigRequest) { r.URL = "http://opa:81 81/v1" }), "url: it must be an absolute"},

		{"port lower bound", valid(func(r *openapi.OPAConfigRequest) { r.URL = "http://opa:1/v1/data" }), ""},
		{"port upper bound", valid(func(r *openapi.OPAConfigRequest) { r.URL = "http://opa:65535/v1/data" }), ""},
		{"port zero", valid(func(r *openapi.OPAConfigRequest) { r.URL = "http://opa:0/v1/data" }), "url: the port must be between"},
		{"port above range", valid(func(r *openapi.OPAConfigRequest) { r.URL = "http://opa:65536/v1/data" }), "url: the port must be between"},

		{"credentials in url", valid(func(r *openapi.OPAConfigRequest) {
			r.URL = "http://user:secret@opa:8181/v1/data"
		}), "url: it must not embed credentials"},
		{"username only in url", valid(func(r *openapi.OPAConfigRequest) {
			r.URL = "http://user@opa:8181/v1/data"
		}), "url: it must not embed credentials"},

		{"timeout zero uses sidecar default", valid(func(r *openapi.OPAConfigRequest) { r.TimeoutSec = 0 }), ""},
		{"timeout upper bound", valid(func(r *openapi.OPAConfigRequest) { r.TimeoutSec = 300 }), ""},
		{"timeout negative", valid(func(r *openapi.OPAConfigRequest) { r.TimeoutSec = -1 }), "timeout_sec: it must be between"},
		{"timeout above range", valid(func(r *openapi.OPAConfigRequest) { r.TimeoutSec = 301 }), "timeout_sec: it must be between"},

		{"gate refused", valid(func(r *openapi.OPAConfigRequest) { r.Gate = true }), "gate: not supported yet"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOPAConfigRequest(tt.req)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("want no error, got %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("want error containing %q, got nil", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}
