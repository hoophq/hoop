package services

import "testing"

// The enterprise split is a product contract: compliance profiles and the
// Balanced/Maximum protection levels are paid; Essential Guardrails and
// manual configuration remain available on the OSS license.
func TestProtectionProfileLicenseTiers(t *testing.T) {
	enterprise := map[string]bool{
		ProtectionProfileHipaaReady:           true,
		ProtectionProfileSoc2Type2:            true,
		ProtectionProfileProtectionMedium:     true,
		ProtectionProfileProtectionHigh:       true,
		ProtectionProfileProtectionPermissive: false,
	}
	if len(enterprise) != len(protectionProfileCatalog) {
		t.Fatalf("license tier map covers %d profiles, catalog has %d — update both together",
			len(enterprise), len(protectionProfileCatalog))
	}
	for id, want := range enterprise {
		if !IsValidProtectionProfile(id) {
			t.Fatalf("profile %q missing from catalog", id)
		}
		if got := IsEnterpriseProtectionProfile(id); got != want {
			t.Errorf("IsEnterpriseProtectionProfile(%q) = %v, want %v", id, got, want)
		}
	}
	if IsEnterpriseProtectionProfile("not-a-profile") {
		t.Error("unknown profile must not be flagged enterprise")
	}

	for id := range protectionProfileCatalog {
		if attr := ProtectionProfileAttributeName(&id); attr == nil || *attr == "" {
			t.Errorf("ProtectionProfileAttributeName(%q) = %v, want non-empty", id, attr)
		}
	}
	if ProtectionProfileAttributeName(nil) != nil {
		t.Error("nil profile must resolve to nil attribute (manual configuration)")
	}
	bogus := "not-a-profile"
	if ProtectionProfileAttributeName(&bogus) != nil {
		t.Error("unknown profile must resolve to nil attribute")
	}
}
