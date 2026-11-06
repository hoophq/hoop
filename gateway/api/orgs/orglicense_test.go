package apiorgs

import (
	"slices"
	"testing"

	"github.com/hoophq/hoop/common/license"
)

// A dropped or renamed property breaks the dashboards without failing a build.
func TestSignedLicenseProperties(t *testing.T) {
	l := &license.License{
		Payload: license.Payload{
			Type:         license.EnterpriseType,
			Description:  "acme corp",
			AllowedHosts: []string{"*"},
			IssuedAt:     1755561600,
			ExpireAt:     1758153600,
		},
		KeyID: "key-id",
	}

	props := signedLicenseProperties("org-1", SignRequest{ExpireAt: "720h"}, l)

	want := map[string]any{
		"org-id":        "org-1",
		"license-type":  license.EnterpriseType,
		"description":   "acme corp",
		"allowed-hosts": []string{"*"},
		"features":      []string{},
		"key-id":        "key-id",
		"issued-at":     "2025-08-19T00:00:00Z",
		"expire-at":     "2025-09-18T00:00:00Z",
		"valid-for":     "720h",
	}
	for key, wantValue := range want {
		got, ok := props[key]
		if !ok {
			t.Errorf("missing property %q", key)
			continue
		}
		if gotSlice, isSlice := got.([]string); isSlice {
			if !slices.Equal(gotSlice, wantValue.([]string)) {
				t.Errorf("property %q = %v, want %v", key, got, wantValue)
			}
			continue
		}
		if got != wantValue {
			t.Errorf("property %q = %v, want %v", key, got, wantValue)
		}
	}
}

// A valid document cannot be produced here: the signing key is not in the
// repository. These are the states that MUST be refused at boot.
func TestVerifyOrgLicenseRefuses(t *testing.T) {
	for _, tt := range []struct {
		msg  string
		data string
	}{
		{"malformed document", `{"payload":`},
		{"enterprise type without a valid signature", `{"payload":{"type":"enterprise","issued_at":1,"expire_at":4102444800,"allowed_hosts":["*"],"description":"d"},"key_id":"k","signature":"c2ln"}`},
		{"unknown license type", `{"payload":{"type":"trial","issued_at":1,"expire_at":4102444800,"allowed_hosts":["*"],"description":"d"},"key_id":"k","signature":"c2ln"}`},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			if _, err := VerifyOrgLicense([]byte(tt.data)); err == nil {
				t.Fatal("expected VerifyOrgLicense to refuse, got nil")
			}
		})
	}
}
