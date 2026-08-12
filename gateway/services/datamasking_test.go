package services

import (
	"strings"
	"testing"
)

func TestGetDataMaskingRulesForConnectionRejectsInvalidOrganizationID(t *testing.T) {
	result, err := GetDataMaskingRulesForConnection("not-a-uuid", "example")
	if err == nil {
		t.Fatalf("invalid organization ID returned rules %s; want an error", result)
	}
	if !strings.Contains(err.Error(), "invalid organization ID") {
		t.Fatalf("unexpected error: %v", err)
	}
}
