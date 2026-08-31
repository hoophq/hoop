package alcatraz_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	alcz "github.com/hoophq/alcatraz/entities"
	"github.com/hoophq/hoop/sidecar/pii/alcatraz"
)

// --- the pii section -------------------------------------------------------

// An omitted section is not "detection off". It used to be, and a lane that
// wanted masking had to hand-write an entity list first, so six shipped
// configs carried the same copied list under the same copied comment. The
// section's job is to subtract, not to switch the product on.
func TestPluginFromConfigAbsentSectionIsPermissive(t *testing.T) {
	p, err := alcatraz.PluginFromConfig(nil)
	if err != nil {
		t.Fatalf("PluginFromConfig(nil): %v", err)
	}
	if p == nil {
		t.Fatal("want a working plugin: a nil one now means this build linked no detector")
	}

	// 51 alcatraz built-ins plus AWS_ACCESS_KEY, JWT and PRIVATE_KEY from
	// secrets.go. The number is quoted in this package's documentation and
	// in the sidecar's, so pin it: an alcatraz upgrade that moves it is
	// exactly when those sentences need rewriting.
	if got := len(p.Entities()); got != 54 {
		t.Errorf("Entities() = %d, want 54 (51 built-ins plus the three in secrets.go)", got)
	}
}

// The two things an absent section used to refuse: detecting, and building a
// masker to rewrite what was detected.
func TestPermissivePluginDetectsAndMasks(t *testing.T) {
	p, err := alcatraz.PluginFromConfig(nil)
	if err != nil {
		t.Fatalf("PluginFromConfig(nil): %v", err)
	}

	if found := p.ScanText("card 4111111111111111"); !slices.Contains(found, alcz.CreditCard) {
		t.Errorf("ScanText = %v, want CREDIT_CARD", found)
	}

	m, err := p.BuildMasker([]byte(`[{"entities":["CREDIT_CARD"],"strategy":"redact"}]`))
	if err != nil {
		t.Fatalf("BuildMasker: %v", err)
	}
	out, _, n := m.Mask([]byte("card 4111111111111111 order 457555462"))
	if strings.Contains(string(out), "4111111111111111") {
		t.Errorf("the card survived masking: %q", out)
	}
	// The permissive detector has US_SSN active, and a bare nine-digit order
	// id IS a valid SSN to any detector. It survives anyway, because the
	// masker scans for the entities its own rules name and nothing else.
	// This is the whole argument for the permissive default.
	if !strings.Contains(string(out), "457555462") {
		t.Errorf("an entity no rule named was rewritten: %q", out)
	}
	if n != 1 {
		t.Errorf("masked %d spans, want 1", n)
	}
}

// An empty JSON object is the same as no section at all: the operator wrote
// the key and subtracted nothing.
func TestPluginFromConfigEmptySectionIsPermissive(t *testing.T) {
	p, err := alcatraz.PluginFromConfig(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("PluginFromConfig: %v", err)
	}
	if got, want := len(p.Entities()), len(alcatraz.AllEntities()); got != want {
		t.Errorf("Entities() = %d, want the full set of %d", got, want)
	}
}

// A section that names entities still restricts, which is what a deployment
// that knows its schema should write.
func TestPluginFromConfigNamedEntitiesRestrict(t *testing.T) {
	p, err := alcatraz.PluginFromConfig(json.RawMessage(`{"entities":["BR_CPF","IBAN_CODE"]}`))
	if err != nil {
		t.Fatalf("PluginFromConfig: %v", err)
	}
	want := []string{alcz.BRCPF, alcz.IBANCode} // sorted
	if got := p.Entities(); !slices.Equal(got, want) {
		t.Errorf("Entities() = %v, want %v", got, want)
	}
}

// The recommended knob for the permissive form: name the recognizers this
// deployment's ordinary data trips.
func TestPluginFromConfigIgnoredSubtracts(t *testing.T) {
	p, err := alcatraz.PluginFromConfig(json.RawMessage(`{"ignored":["US_SSN","DATE_TIME","URL"]}`))
	if err != nil {
		t.Fatalf("PluginFromConfig: %v", err)
	}
	if got, want := len(p.Entities()), len(alcatraz.AllEntities())-3; got != want {
		t.Errorf("Entities() = %d types, want %d", got, want)
	}
	if slices.Contains(p.Entities(), alcz.USSSN) {
		t.Error("US_SSN was ignored and must not be active")
	}
}

// A present but invalid section is an error, so an operator who wrote entity
// names hears that they do not resolve instead of watching masking quietly do
// nothing. Both failures wear the section's name.
func TestPluginFromConfigInvalidSection(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"malformed json", `{"entities":`},
		{"wrong type", `{"entities":"US_SSN"}`},
		{"unknown entity", `{"entities":["US_SSSN"]}`},
		{"ignored everything", `{"entities":["US_SSN"],"ignored":["US_SSN"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := alcatraz.PluginFromConfig(json.RawMessage(tc.raw))
			if err == nil {
				t.Fatalf("want an error for %s", tc.raw)
			}
			if p != nil {
				t.Error("a failed section must not yield a plugin")
			}
			if !strings.Contains(err.Error(), "pii section") {
				t.Errorf("error should name the section: %v", err)
			}
		})
	}
}
