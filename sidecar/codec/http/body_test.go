package http_test

import (
	"reflect"
	"slices"
	"testing"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	libhttp "github.com/hoophq/libhoop/v2/codec/http"
)

// CaptureRequestBody keeps what the client sends and drops what the server
// answers. CaptureBody keeps both.
func TestCaptureRequestBodyKeepsOnlyTheRequestSide(t *testing.T) {
	const resp = "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nsent"
	for _, tc := range []struct {
		name     string
		opts     codechttp.Options
		wantResp string
	}{
		{"request only", codechttp.Options{CaptureRequestBody: true}, ""},
		{"both", codechttp.Options{CaptureBody: true, CaptureRequestBody: true}, "sent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := codechttp.New(tc.opts)
			req := decodeRequest(t, c, "POST /x HTTP/1.1\r\nHost: h\r\nContent-Length: 2\r\n\r\n{}")
			if req.HTTP.Body != "{}" {
				t.Errorf("request body %q, want {}", req.HTTP.Body)
			}
			stmts, _, err := c.Decode(inspect.FromServer, []byte(resp))
			if err != nil || len(stmts) != 1 {
				t.Fatalf("Decode: %d statements, %v", len(stmts), err)
			}
			if got := stmts[0].HTTP.Body; got != tc.wantResp {
				t.Errorf("response body %q, want %q", got, tc.wantResp)
			}
		})
	}
}

// Pins the reason for dropResponseBody: libhoop has one switch for both
// directions. A new option means libhoop may now capture requests alone;
// use it and delete the shim.
func TestLibhoopStillCapturesBothDirectionsOrNeither(t *testing.T) {
	want := []string{"CaptureBody", "MaxBodyBytes", "Headers", "SensitiveQueryParams", "MaxMessageBytes"}
	typ := reflect.TypeOf(libhttp.Options{})
	var got []string
	for i := range typ.NumField() {
		got = append(got, typ.Field(i).Name)
	}
	if !slices.Equal(got, want) {
		t.Errorf("libhoop codec options are %v, want %v; if one captures request bodies alone, use it and delete dropResponseBody", got, want)
	}
}
