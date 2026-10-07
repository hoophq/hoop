package services

import (
	"testing"

	"github.com/google/uuid"
)

// A save that names no limit must return before any SQL: a gateway passes nil
// on every analyzer save. The nil handle panics on use.
func TestApplyAnalyzerApprovalTTLsReturnsBeforeTheDatabase(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("the call touched the database: %v", r)
		}
	}()
	if err := ApplyAnalyzerApprovalTTLs(nil, uuid.New(), "hold-writes", nil, nil); err != nil {
		t.Fatalf("no limit: unexpected error %v", err)
	}
}
