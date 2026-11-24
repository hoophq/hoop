package apiroutes

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAPIKeyOrgIDIsCanonical(t *testing.T) {
	const orgID = "0f6c1d2e-3a4b-4c5d-8e9f-a0b1c2d3e4f5"
	for _, tc := range []struct{ apiKey, serverOrgID, want string }{
		{orgID + "|secret", "", orgID},
		{strings.ToUpper(orgID) + "|secret", "", orgID},
		{"{" + orgID + "}|secret", "", orgID},
		{strings.ReplaceAll(orgID, "-", "") + "|secret", "", orgID},
		{"xapi-secret", strings.ToUpper(orgID), orgID},
		{"not-a-uuid|secret", "", "not-a-uuid"},
	} {
		assert.Equal(t, tc.want, apiKeyOrgID(tc.apiKey, tc.serverOrgID), "api key %q", tc.apiKey)
	}
}
