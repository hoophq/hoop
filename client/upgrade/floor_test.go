package upgrade

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateInstallableVersion(t *testing.T) {
	// Sanity: the floor constant must itself be valid semver so we don't
	// silently break the message formatter.
	if MinInstallableMinor == "" || MinInstallableMinor[0] != 'v' {
		t.Fatalf("MinInstallableMinor must be a semver-prefixed string, got %q", MinInstallableMinor)
	}

	cases := []struct {
		in      string
		want    error // nil means must pass; otherwise must wrap this sentinel
	}{
		// Floor and above: allowed.
		{"1.74.0", nil},
		{"1.74.0-rc.1", nil},
		{"1.74.5", nil},
		{"1.75.0", nil},
		{"2.0.0", nil},
		{"2.5.7-beta.2", nil},

		// Below the floor: ErrBelowFloor.
		{"1.73.0", ErrBelowFloor},
		{"1.72.0", ErrBelowFloor},
		{"1.0.0", ErrBelowFloor},
		{"0.99.99", ErrBelowFloor},
		{"1.73.0-rc.1", ErrBelowFloor},

		// Dev-build sentinel: ErrUnknownGatewayVersion.
		{"", ErrUnknownGatewayVersion},
		{"unknown", ErrUnknownGatewayVersion},

		// Partial-but-valid semver canonicalises to .0 patches; well below
		// the floor — accepted as ErrBelowFloor, not ErrInvalidVersion.
		{"1", ErrBelowFloor},
		{"1.2", ErrBelowFloor},

		// Truly malformed: ErrInvalidVersion.
		{"banana", ErrInvalidVersion},
		{"1.2.3.4", ErrInvalidVersion},
		{"1.2.x", ErrInvalidVersion},
		{"v1.74.0", ErrInvalidVersion}, // caller must normalize first
	}
	for _, tc := range cases {
		err := ValidateInstallableVersion(tc.in)
		switch {
		case tc.want == nil:
			if err != nil {
				t.Errorf("ValidateInstallableVersion(%q): want nil, got %v", tc.in, err)
			}
		default:
			if err == nil {
				t.Errorf("ValidateInstallableVersion(%q): want error wrapping %v, got nil", tc.in, tc.want)
				continue
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("ValidateInstallableVersion(%q): want errors.Is(%v), got %v", tc.in, tc.want, err)
			}
		}
	}
}

func TestValidateInstallableVersionBelowFloorMessage(t *testing.T) {
	err := ValidateInstallableVersion("1.50.0")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrBelowFloor) {
		t.Fatalf("expected ErrBelowFloor sentinel")
	}
	floor := MinInstallableMinor[1:] // "1.74"
	if !strings.Contains(err.Error(), floor) {
		t.Errorf("message should mention the floor (%s): %v", floor, err)
	}
	if !strings.Contains(err.Error(), "1.50.0") {
		t.Errorf("message should mention the rejected version 1.50.0: %v", err)
	}
}

func TestValidateInstallableVersionUnknownMessage(t *testing.T) {
	err := ValidateInstallableVersion("unknown")
	if !errors.Is(err, ErrUnknownGatewayVersion) {
		t.Fatalf("expected ErrUnknownGatewayVersion sentinel, got %v", err)
	}
	if !strings.Contains(err.Error(), `"unknown"`) {
		t.Errorf("message should quote the bad value: %v", err)
	}
}
