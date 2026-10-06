package services

import "fmt"

// The bounds of a sidecar review limit, in seconds. The relay polls a held
// review every 5 s, so a shorter limit is noise.
const (
	SidecarReviewTTLMinSec = 60
	SidecarReviewTTLMaxSec = 604800
)

// SidecarReviewTTLError is a request limit out of bounds. The API answers 422.
type SidecarReviewTTLError struct{ msg string }

func (e *SidecarReviewTTLError) Error() string { return e.msg }

// NormalizeSidecarReviewTTL checks a request value: nil is absent, 0 is clear,
// else it must be in bounds. It returns a copy.
func NormalizeSidecarReviewTTL(field string, v *int) (*int, error) {
	if v == nil {
		return nil, nil
	}
	n := *v
	if n != 0 && (n < SidecarReviewTTLMinSec || n > SidecarReviewTTLMaxSec) {
		return nil, &SidecarReviewTTLError{fmt.Sprintf("%s must be between %d and %d seconds, or 0 for no limit",
			field, SidecarReviewTTLMinSec, SidecarReviewTTLMaxSec)}
	}
	return &n, nil
}

// StoredSidecarReviewTTL maps a normalized value to what a rule stores: 0 is nil.
func StoredSidecarReviewTTL(v *int) *int {
	if v == nil || *v == 0 {
		return nil
	}
	n := *v
	return &n
}

// ApplySidecarReviewTTL is an update: an absent value keeps stored, 0 clears it.
func ApplySidecarReviewTTL(stored, req *int) *int {
	if req == nil {
		return stored
	}
	return StoredSidecarReviewTTL(req)
}

// CheckSidecarReviewTTL checks a stored value. nil passes; 0 is out of bounds.
func CheckSidecarReviewTTL(field string, v *int) error {
	if v == nil {
		return nil
	}
	if *v < SidecarReviewTTLMinSec || *v > SidecarReviewTTLMaxSec {
		return fmt.Errorf("%s must be between %d and %d seconds, got %d",
			field, SidecarReviewTTLMinSec, SidecarReviewTTLMaxSec, *v)
	}
	return nil
}
