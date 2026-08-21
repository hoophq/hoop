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
