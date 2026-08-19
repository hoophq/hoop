package apiserverinfo

import (
	"errors"
	"fmt"
	"testing"

	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/gateway/api/openapi"
)

func TestLicenseStatus(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"verified license is valid", nil, openapi.LicenseStatusValid},
		{"expired sentinel maps to expired", license.ErrExpired, openapi.LicenseStatusExpired},
		{
			"expired sentinel is matched through a wrap",
			fmt.Errorf("failed parsing license: %w", license.ErrExpired),
			openapi.LicenseStatusExpired,
		},
		{"host mismatch is invalid", errors.New(`host "acme.org" is not allowed`), openapi.LicenseStatusInvalid},
		{"used before issued is invalid", license.ErrUsedBeforeIssued, openapi.LicenseStatusInvalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := licenseStatus(tt.err); got != tt.want {
				t.Errorf("licenseStatus(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
