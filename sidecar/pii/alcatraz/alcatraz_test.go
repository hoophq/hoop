package alcatraz_test

import (
	"slices"
	"strings"
	"sync"
	"testing"

	alcz "github.com/hoophq/alcatraz/entities"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/pii/alcatraz"
	"github.com/hoophq/hoop/sidecar/policy"
)

// --- the seams -------------------------------------------------------------

// newDet builds a Detector or fails the test.
func newDet(t *testing.T, o alcatraz.Options) *alcatraz.Detector {
	t.Helper()
	d, err := alcatraz.NewDetector(o)
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}
	return d
}

// newMask builds a Masker over the detector or fails the test.
func newMask(t *testing.T, d *alcatraz.Detector, rules ...alcatraz.Rule) *alcatraz.Masker {
	t.Helper()
	m, err := alcatraz.NewMasker(d, rules)
	if err != nil {
		t.Fatalf("NewMasker: %v", err)
	}
	return m
}

func TestSatisfiesPolicyScanner(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.USSSN}})
	var _ policy.Scanner = d
}

// Checksum-verified national identifiers, the reason this module exists.
func TestMasksEntitiesTheBuiltinsCannot(t *testing.T) {
	det := newDet(t, alcatraz.Options{
		Entities: []string{alcz.BRCPF, alcz.IBANCode, alcz.UKNINO},
	})
	m := newMask(t, det,
		alcatraz.Rule{Entity: alcz.BRCPF, Strategy: alcatraz.StrategyRedact},
		alcatraz.Rule{Entity: alcz.IBANCode, Strategy: alcatraz.StrategyRedact})

	out, res := m.MaskString("cpf 111.444.777-35 iban GB82WEST12345698765432 done")
	if strings.Contains(out, "111.444.777-35") {
		t.Errorf("CPF survived masking: %q", out)
	}
	if strings.Contains(out, "GB82WEST12345698765432") {
		t.Errorf("IBAN survived masking: %q", out)
	}
	if res.Count != 2 {
		t.Errorf("Count = %d, want 2", res.Count)
	}
	if !slices.Contains(res.Entities, alcz.BRCPF) {
		t.Errorf("Entities = %v, want BR_CPF", res.Entities)
	}
}

// A checksum-verified detector is the reason to prefer this over a regex: a
// digit run that fails mod-11 must NOT be masked, or every order id goes too.
func TestChecksumRejectsLookalikes(t *testing.T) {
	det := newDet(t, alcatraz.Options{Entities: []string{alcz.BRCPF}})
	m := newMask(t, det, alcatraz.Rule{Entity: alcz.BRCPF, Strategy: alcatraz.StrategyRedact})

	// 111.444.777-35 is a valid CPF; 111.444.777-99 fails the check digits.
	out, res := m.MaskString("good 111.444.777-35 bad 111.444.777-99")
	if strings.Contains(out, "111.444.777-35") {
		t.Errorf("valid CPF not masked: %q", out)
	}
	if !strings.Contains(out, "111.444.777-99") {
		t.Errorf("invalid CPF was masked; the checksum is not being applied: %q", out)
	}
	if res.Count != 1 {
		t.Errorf("Count = %d, want 1", res.Count)
	}
}

// --- entity set resolution -------------------------------------------------

func TestEntitiesIsRequired(t *testing.T) {
	_, err := alcatraz.NewDetector(alcatraz.Options{})
	if err == nil {
		t.Fatal("want an error: an all-entities default rewrites ordinary numeric columns")
	}
	if !strings.Contains(err.Error(), "Entities") {
		t.Errorf("error should name the missing field: %v", err)
	}
}

// The full set is available, but only by asking for it by name.
func TestAllEntitiesOptIn(t *testing.T) {
	all := alcatraz.AllEntities()
	if len(all) < 40 {
		t.Fatalf("AllEntities returned %d types, want the full recognizer set", len(all))
	}
	// NER-only entities must not appear: this package wires the pattern core.
	for _, ner := range []string{alcz.Person, alcz.Location, alcz.NRP} {
		if slices.Contains(all, ner) {
			t.Errorf("%s needs the ner module and must not be advertised", ner)
		}
	}
	d := newDet(t, alcatraz.Options{Entities: all})
	if len(d.Entities()) != len(all) {
		t.Errorf("Entities() = %d, want %d", len(d.Entities()), len(all))
	}
}

// A typo, or an NER entity this package does not wire, must fail at
// construction rather than silently detecting nothing.
func TestUnknownEntityRejected(t *testing.T) {
	for _, bad := range []string{"US_SSSN", alcz.Person} {
		_, err := alcatraz.NewDetector(alcatraz.Options{Entities: []string{bad}})
		if err == nil {
			t.Errorf("%q: want an error", bad)
			continue
		}
		if !strings.Contains(err.Error(), bad) {
			t.Errorf("%q: error should name the entity: %v", bad, err)
		}
	}
}

// The measured false-positive rates are a load-bearing claim in this
// package's documentation. Pin the worst one so the doc cannot rot.
func TestNoisyDocumentsTheRealOffenders(t *testing.T) {
	if _, ok := alcatraz.Noisy[alcz.USSSN]; !ok {
		t.Error("US_SSN must be documented as noisy: it fires on ~a third of 9-digit ids")
	}
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.USSSN}})
	// A plain order id. No checksum can reject it, so it IS detected. The
	// documentation says so, and this test proves the documentation.
	if got := d.Find(alcz.USSSN, []byte("457555462")); len(got) == 0 {
		t.Error("US_SSN no longer matches a bare 9-digit id; the Noisy entry is now wrong")
	}
}

func TestExplicitEntitiesRestrictTheSet(t *testing.T) {
	d := newDet(t, alcatraz.Options{
		Entities: []string{alcz.USSSN, alcz.CreditCard},
	})
	want := []string{alcz.CreditCard, alcz.USSSN} // sorted
	if got := d.Entities(); !slices.Equal(got, want) {
		t.Errorf("Entities() = %v, want %v", got, want)
	}
}

// Entities() must hand out a copy, so a caller reslicing it cannot reshape
// the Detector that every connection shares.
func TestEntitiesReturnsACopy(t *testing.T) {
	d := newDet(t, alcatraz.Options{
		Entities: []string{alcz.USSSN, alcz.CreditCard},
	})
	got := d.Entities()
	got[0] = "CLOBBERED"
	if d.Entities()[0] == "CLOBBERED" {
		t.Error("Entities() aliases internal state")
	}
}

// NewDetector must not sort or otherwise mutate the caller's slice.
func TestOptionsSliceNotMutated(t *testing.T) {
	in := []string{alcz.USSSN, alcz.CreditCard, alcz.IBANCode}
	orig := slices.Clone(in)
	newDet(t, alcatraz.Options{Entities: in})
	if !slices.Equal(in, orig) {
		t.Errorf("caller's Entities slice was mutated: %v, was %v", in, orig)
	}
}

// --- thresholds and allow lists --------------------------------------------

func TestAllowListSuppressesFixtures(t *testing.T) {
	const testCard = "4111111111111111"
	d := newDet(t, alcatraz.Options{
		Entities:  []string{alcz.CreditCard},
		AllowList: []string{testCard},
	})
	if got := d.Find(alcz.CreditCard, []byte("card "+testCard)); len(got) != 0 {
		t.Errorf("allow-listed value was still detected: %v", got)
	}
	// A different valid card must still be caught.
	if got := d.Find(alcz.CreditCard, []byte("card 5500005555555559")); len(got) != 1 {
		t.Errorf("allow list suppressed an unrelated card: %v", got)
	}
}

// --- Find contract ---------------------------------------------------------

// Spans must be valid byte offsets into the input, ascending, so the Masker
// can slice with them.
func TestFindReturnsUsableAscendingSpans(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.CreditCard}})
	data := []byte("a 4111111111111112 b 5500005555555559 c 4012888888881881 d")

	spans := d.Find(alcz.CreditCard, data)
	if len(spans) < 2 {
		t.Fatalf("want several cards, got %d", len(spans))
	}
	for i, sp := range spans {
		if sp[0] < 0 || sp[1] > len(data) || sp[0] >= sp[1] {
			t.Fatalf("span %d out of range: %v (len %d)", i, sp, len(data))
		}
		if i > 0 && spans[i-1][0] >= sp[0] {
			t.Errorf("spans not ascending: %v then %v", spans[i-1], sp)
		}
	}
}

// Offsets must be BYTES, not runes: a multi-byte prefix must not shift them.
func TestFindOffsetsAreBytes(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.EmailAddress}})
	data := []byte("héllo — wörld ada@example.com")

	spans := d.Find(alcz.EmailAddress, data)
	if len(spans) != 1 {
		t.Fatalf("want 1 email, got %d", len(spans))
	}
	if got := string(data[spans[0][0]:spans[0][1]]); got != "ada@example.com" {
		t.Errorf("span addresses %q, not the email; offsets are not byte indices", got)
	}
}

func TestFindUnclaimedEntityReturnsNil(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.USSSN}})
	if got := d.Find(alcz.CreditCard, []byte("card 4111111111111111")); got != nil {
		t.Errorf("Find on an unclaimed entity returned %v", got)
	}
}

func TestFindEmptyInput(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.USSSN}})
	if got := d.Find(alcz.USSSN, nil); got != nil {
		t.Errorf("Find(nil) = %v", got)
	}
}

// --- ScanText / policy -----------------------------------------------------

func TestScanTextNamesClassesSorted(t *testing.T) {
	d := newDet(t, alcatraz.Options{
		Entities: []string{alcz.USSSN, alcz.CreditCard, alcz.EmailAddress},
	})
	got := d.ScanText("ssn 457-55-5462 card 4111111111111111 mail ada@example.com")

	want := []string{alcz.CreditCard, alcz.EmailAddress, alcz.USSSN}
	if !slices.Equal(got, want) {
		t.Errorf("ScanText = %v, want %v", got, want)
	}
}

func TestScanTextCleanAndEmpty(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.USSSN, alcz.CreditCard}})
	if got := d.ScanText(""); got != nil {
		t.Errorf("ScanText(\"\") = %v", got)
	}
	if got := d.ScanText("SELECT name FROM customers WHERE id = 1"); len(got) != 0 {
		t.Errorf("clean SQL reported entities: %v", got)
	}
}

// The guardrail case end to end: a query embedding a national ID is denied,
// and the denial names the class without quoting the value.
func TestPolicyDeniesStatementCarryingPII(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.BRCPF}})
	rules, err := policy.NewRulesWithScanner([]policy.Rule{{
		Name:     "no-cpf-in-query",
		Type:     policy.MatchPII,
		Entities: []string{alcz.BRCPF},
	}}, d)
	if err != nil {
		t.Fatalf("NewRulesWithScanner: %v", err)
	}

	stmt := inspect.Statement{
		Protocol:  inspect.Postgres,
		Direction: inspect.FromClient,
		Operation: inspect.OpSelect,
		Text:      "SELECT * FROM people WHERE cpf = '111.444.777-35'",
	}
	v := rules.Evaluate(stmt)
	if !v.Denied {
		t.Fatal("query embedding a CPF was allowed")
	}
	if !strings.Contains(v.Message, alcz.BRCPF) {
		t.Errorf("message should name the class: %q", v.Message)
	}
	if strings.Contains(v.Message, "111.444.777-35") {
		t.Errorf("denial message leaked the value: %q", v.Message)
	}

	clean := stmt
	clean.Text = "SELECT * FROM people WHERE id = 1"
	if v := rules.Evaluate(clean); v.Denied {
		t.Errorf("clean query denied: %+v", v)
	}
}

// --- concurrency -----------------------------------------------------------

// One Detector is shared by every connection through a single Masker.
func TestConcurrentUse(t *testing.T) {
	d := newDet(t, alcatraz.Options{
		Entities: []string{alcz.CreditCard, alcz.BRCPF},
	})
	m := newMask(t, d,
		alcatraz.Rule{Entity: alcz.CreditCard, Strategy: alcatraz.StrategyPartial, KeepLast: 4},
		alcatraz.Rule{Entity: alcz.BRCPF, Strategy: alcatraz.StrategyRedact})

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, res := m.MaskString("card 4111111111111111 cpf 111.444.777-35")
			if res.Count != 2 {
				t.Errorf("Count = %d, want 2 (%q)", res.Count, out)
			}
			if strings.Contains(out, "4111111111111111") {
				t.Errorf("card survived: %q", out)
			}
			// Partial masking keeps the last four.
			if !strings.Contains(out, "1111") {
				t.Errorf("partial strategy lost the tail: %q", out)
			}
			_ = d.ScanText("ssn 457-55-5462")
		}()
	}
	wg.Wait()
}
