package services

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A save that names no limit, and a refused limit, must return before any SQL:
// a gateway passes nil on every analyzer save. The nil handle panics on use.
func TestApplyAnalyzerApprovalTTLsReturnsBeforeTheDatabase(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("the call touched the database: %v", r)
		}
	}()
	if err := ApplyAnalyzerApprovalTTLs(nil, uuid.New(), "hold-writes", nil, nil); err != nil {
		t.Fatalf("no limit: unexpected error %v", err)
	}
	err := ApplyAnalyzerApprovalTTLs(nil, uuid.New(), "hold-writes", nil, ttlSec(30))
	if err == nil || !strings.Contains(err.Error(), "approval_ttl_sec") {
		t.Fatalf("30 s: want an error naming approval_ttl_sec, got %v", err)
	}
}
