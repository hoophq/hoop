package services

import (
	"errors"
	"strings"
	"testing"
)

func ttlSec(n int) *int { return &n }

func TestNormalizeSidecarReviewTTL(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   *int
		want *int
		err  bool
	}{
		{name: "absent", in: nil, want: nil},
		{name: "clear", in: ttlSec(0), want: ttlSec(0)},
		{name: "negative", in: ttlSec(-1), err: true},
		{name: "below the minimum", in: ttlSec(SidecarReviewTTLMinSec - 1), err: true},
		{name: "the minimum", in: ttlSec(SidecarReviewTTLMinSec), want: ttlSec(SidecarReviewTTLMinSec)},
		{name: "the maximum", in: ttlSec(SidecarReviewTTLMaxSec), want: ttlSec(SidecarReviewTTLMaxSec)},
		{name: "above the maximum", in: ttlSec(SidecarReviewTTLMaxSec + 1), err: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeSidecarReviewTTL("pending_ttl_sec", tt.in)
			if tt.err {
				if err == nil || !strings.Contains(err.Error(), "pending_ttl_sec") {
					t.Fatalf("want an error naming pending_ttl_sec, got %v", err)
				}
				// The analyzer routes answer 422 only for this type.
				var limitErr *SidecarReviewTTLError
				if !errors.As(err, &limitErr) {
					t.Errorf("want a *SidecarReviewTTLError, got %T", err)
				}
				if got != nil {
					t.Errorf("want nil with the error, got %d", *got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			// A copy: the caller may keep the request value and the result apart.
			if got != nil && got == tt.in {
				t.Error("the result aliases the request value")
			}
		})
	}
}

func TestApplySidecarReviewTTL(t *testing.T) {
	stored := ttlSec(900)
	if got := ApplySidecarReviewTTL(stored, nil); got != stored {
		t.Errorf("absent must keep the stored value, got %v", got)
	}
	if got := ApplySidecarReviewTTL(stored, ttlSec(0)); got != nil {
		t.Errorf("0 must clear the stored value, got %d", *got)
	}
	if got := ApplySidecarReviewTTL(stored, ttlSec(600)); got == nil || *got != 600 {
		t.Errorf("a value must replace the stored one, got %v", got)
	}
	if got := ApplySidecarReviewTTL(nil, nil); got != nil {
		t.Errorf("absent over no limit must stay no limit, got %d", *got)
	}
	// A rule never stores 0: it would read back as a limit of 0 seconds.
	if got := StoredSidecarReviewTTL(ttlSec(0)); got != nil {
		t.Errorf("StoredSidecarReviewTTL(0) = %d, want nil", *got)
	}
}

func TestCheckSidecarReviewTTL(t *testing.T) {
	if err := CheckSidecarReviewTTL("approval_ttl_sec", nil); err != nil {
		t.Errorf("no limit must pass, got %v", err)
	}
	for _, n := range []int{0, SidecarReviewTTLMinSec - 1, SidecarReviewTTLMaxSec + 1} {
		if err := CheckSidecarReviewTTL("approval_ttl_sec", ttlSec(n)); err == nil || !strings.Contains(err.Error(), "approval_ttl_sec") {
			t.Errorf("stored %d: want an error naming approval_ttl_sec, got %v", n, err)
		}
	}
	for _, n := range []int{SidecarReviewTTLMinSec, SidecarReviewTTLMaxSec} {
		if err := CheckSidecarReviewTTL("approval_ttl_sec", ttlSec(n)); err != nil {
			t.Errorf("stored %d: unexpected error %v", n, err)
		}
	}
}
