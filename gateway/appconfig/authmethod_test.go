package appconfig

import (
	"testing"

	idptypes "github.com/hoophq/hoop/gateway/idp/types"
)

// IDP_CLIENT_ASSERTION_FILE alone does not select OIDC. The helm chart sets it
// with idpFederatedToken.enabled for a provider an admin then sets in Settings >
// Authentication, so the gateway must start with local authentication first.
func TestLoadAuthMethodClientAssertionFileAlone(t *testing.T) {
	for _, key := range []string{"AUTH_METHOD", "IDP_ISSUER", "IDP_CLIENT_ID", "IDP_CLIENT_SECRET", "IDP_URI"} {
		t.Setenv(key, "")
	}
	t.Setenv("IDP_CLIENT_ASSERTION_FILE", "/var/run/secrets/hoop/idp/token")
	if got, err := loadAuthMethod(); err != nil || got != idptypes.ProviderTypeLocal {
		t.Fatalf("assertion file alone: got %q, %v; want %q", got, err, idptypes.ProviderTypeLocal)
	}

	t.Setenv("IDP_ISSUER", "https://login.microsoftonline.com/tenant/v2.0")
	if got, err := loadAuthMethod(); err != nil || got != idptypes.ProviderTypeOIDC {
		t.Fatalf("with an issuer: got %q, %v; want %q", got, err, idptypes.ProviderTypeOIDC)
	}
}
