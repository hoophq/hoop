package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/license/licensetest"
)

// The hole this file exists for. A Status assembled by a caller used to be
// enough to lift the caps, because the daemon took a verdict and trusted it.
// Now the fields that decide are unexported and only license.Load sets them,
// so the best a caller can build grants nothing.
func TestAHandBuiltStatusLiftsNothing(t *testing.T) {
	forged := license.Status{
		Source: "an attacker",
		License: &license.License{
			Payload: license.Payload{
				Type:         license.EnterpriseType,
				IssuedAt:     time.Now().Add(-time.Hour).Unix(),
				ExpireAt:     time.Now().Add(10 * 365 * 24 * time.Hour).Unix(),
				AllowedHosts: []string{"*"},
				Description:  "Free Stuff Inc",
			},
			KeyID:     "whatever",
			Signature: "whatever",
		},
	}

	if forged.State() != license.StateInvalid {
		t.Errorf("state = %q, want invalid", forged.State())
	}
	if forged.Allows(license.FeatureGuardrails) || forged.Allows(license.FeatureDataMasking) {
		t.Fatal("a hand-built status granted a feature")
	}
	if c := capsFor(forged); c.guardrails != maxGuardrailRules || c.mask != maxMaskRules {
		t.Errorf("caps moved for a hand-built status: %+v", c)
	}
	if problems := overCap().checkLimits(forged); len(problems) != 2 {
		t.Errorf("a hand-built status lifted a cap: %v", problems)
	}
}

// UseLicense takes a reference and verifies it, so the caller never gets to
// decide. A document nobody signed is refused at the seam rather than stored
// and quietly ignored.
func TestUseLicenseRefusesAnUnsignedDocument(t *testing.T) {
	doc, err := json.Marshal(license.License{
		Payload: license.Payload{
			Type:         license.EnterpriseType,
			IssuedAt:     time.Now().Add(-time.Hour).Unix(),
			ExpireAt:     time.Now().Add(time.Hour).Unix(),
			AllowedHosts: []string{"*"},
			Description:  "Free Stuff Inc",
		},
		KeyID:     "whatever",
		Signature: "bm90LWEtc2lnbmF0dXJl",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	cfg := overCap()
	uerr := cfg.UseLicense(license.Ref{Value: string(doc), Source: "the control plane"})

	if uerr == nil {
		t.Fatal("an unsigned document was adopted")
	}
	if !strings.Contains(uerr.Error(), "the control plane") {
		t.Errorf("the error does not name the source: %v", uerr)
	}
	if cfg.Licensing().State() != license.StateMissing {
		t.Errorf("a refused license was stored anyway: %q", cfg.Licensing().State())
	}
	if len(cfg.checkLimits(cfg.Licensing())) != 2 {
		t.Error("a refused license lifted a cap")
	}
}

// A license signed by a key this build does not trust is the same refusal.
// This is the case a compromised control plane would produce.
func TestUseLicenseRefusesAnotherKeysSignature(t *testing.T) {
	// Sign inside a subtest so its cleanup puts the production key back
	// before the assertion. What is left is a document carrying a real
	// signature from a key this process no longer accepts.
	var doc string
	t.Run("issued under a trust root that goes away", func(t *testing.T) {
		doc = licensetest.Document(t, licensetest.Enterprise())
	})

	cfg := overCap()
	if err := cfg.UseLicense(license.Ref{Value: doc, Source: "the control plane"}); err == nil {
		t.Fatal("a license from an untrusted key was adopted")
	}
	if len(cfg.checkLimits(cfg.Licensing())) != 2 {
		t.Error("a license from an untrusted key lifted a cap")
	}
}

// The seam still has to work for a real one, or the enforcement above is just
// a broken feature.
func TestUseLicenseAdoptsAVerifiedDocument(t *testing.T) {
	cfg := overCap()

	if err := cfg.UseLicense(licensetest.Ref(t, licensetest.Enterprise())); err != nil {
		t.Fatalf("UseLicense refused a signed license: %v", err)
	}
	if cfg.Licensing().State() != license.StateValid {
		t.Fatalf("state = %q, want valid", cfg.Licensing().State())
	}
	if problems := cfg.checkLimits(cfg.Licensing()); len(problems) != 0 {
		t.Errorf("a verified license did not lift the caps: %v", problems)
	}
}

// An expired document is adopted rather than refused: the caps stay in force
// and the process reports the state. Refusing it would leave a control plane
// unable to tell a sidecar that the customer stopped paying.
func TestUseLicenseAdoptsAnExpiredDocument(t *testing.T) {
	cfg := overCap()

	if err := cfg.UseLicense(licensetest.Ref(t, licensetest.Expiring(-time.Hour))); err != nil {
		t.Fatalf("UseLicense refused an expired license: %v", err)
	}
	if cfg.Licensing().State() != license.StateExpired {
		t.Errorf("state = %q, want expired", cfg.Licensing().State())
	}
	if len(cfg.checkLimits(cfg.Licensing())) != 2 {
		t.Error("an expired license lifted a cap")
	}
}
