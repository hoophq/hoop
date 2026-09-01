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
	// The optional narrowing interface, reached through a type assertion in
	// matchesPII, so only a compile-time check catches a drifted signature.
	var _ policy.ScopedScanner = d
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

// An empty Entities is the permissive form rather than a mistake. Enabling a
// recognizer is not the same as scanning for it: NewMasker narrows a response
// scan to the entities its rules name, and ScanTextFor narrows a statement
// scan to the entities a guardrail rule names.
func TestEmptyEntitiesMeansEveryEntity(t *testing.T) {
	d := newDet(t, alcatraz.Options{})

	want := alcatraz.AllEntities()
	slices.Sort(want)
	if got := d.Entities(); !slices.Equal(got, want) {
		t.Errorf("Entities() = %v, want the full set %v", got, want)
	}
}

// Ignored is the knob that pairs with the permissive form: subtract the
// recognizers this deployment's ordinary data trips instead of enumerating
// the several dozen it does not.
func TestIgnoredSubtractsFromThePermissiveSet(t *testing.T) {
	d := newDet(t, alcatraz.Options{Ignored: []string{alcz.USSSN, alcz.URL}})

	got := d.Entities()
	if len(got) != len(alcatraz.AllEntities())-2 {
		t.Errorf("Entities() has %d types, want the full set less two", len(got))
	}
	for _, gone := range []string{alcz.USSSN, alcz.URL} {
		if slices.Contains(got, gone) {
			t.Errorf("%s was ignored and must not be active", gone)
		}
	}
}

// A misspelled Ignored entry is refused. It subtracts nothing, so the
// recognizer the operator wrote it to disable keeps running: the permissive
// default hands back a detector that looks correct and behaves as though the
// section were never written. This is the one config mistake that gets LOUDER
// the more careful the operator was, because writing the section at all means
// they wanted something switched off.
func TestUnknownIgnoredEntityRejected(t *testing.T) {
	// No Entities: the permissive form, where the typo is invisible.
	_, err := alcatraz.NewDetector(alcatraz.Options{Ignored: []string{"US_SSSN"}})
	if err == nil {
		t.Fatal("want an error: US_SSSN ignores nothing and US_SSN stays active")
	}
	if !strings.Contains(err.Error(), "US_SSSN") {
		t.Errorf("error should name the unresolved entry: %v", err)
	}
	if !strings.Contains(err.Error(), "ignored") {
		t.Errorf("error should name the list holding it: %v", err)
	}
}

// One restart per typo is how a rollout takes three rounds. Both lists are
// checked before either is applied, and each unresolved name is attributed to
// the key it was written under.
func TestUnknownEntitiesAndIgnoredReportedTogether(t *testing.T) {
	_, err := alcatraz.NewDetector(alcatraz.Options{
		Entities: []string{alcz.USSSN, "BR_CPFF"},
		Ignored:  []string{"URLL"},
	})
	if err == nil {
		t.Fatal("want an error for both unresolved names")
	}
	for _, want := range []string{"entities: BR_CPFF", "ignored: URLL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

// A name that resolves is not reported just because it appears in both lists.
// Naming a type in Entities and Ignored is how "everything is subtracted"
// gets written, and that already has its own error.
func TestKnownEntityInBothListsIsNotAnUnknownName(t *testing.T) {
	_, err := alcatraz.NewDetector(alcatraz.Options{
		Entities: []string{alcz.USSSN},
		Ignored:  []string{alcz.USSSN},
	})
	if err == nil {
		t.Fatal("want an error: nothing is left to detect")
	}
	if strings.Contains(err.Error(), "unknown") {
		t.Errorf("US_SSN resolves in both lists; the error should be the empty set: %v", err)
	}
}

// Ignoring everything leaves a detector that detects nothing, which is a
// config mistake wearing the costume of a working one.
func TestIgnoringEveryEntityRejected(t *testing.T) {
	_, err := alcatraz.NewDetector(alcatraz.Options{
		Entities: []string{alcz.USSSN},
		Ignored:  []string{alcz.USSSN},
	})
	if err == nil {
		t.Fatal("want an error: nothing is left to detect")
	}
}

// AllEntities is the same set an empty Options.Entities activates, and it
// must exclude the NER classes this package does not wire.
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

// ScanTextFor is the cheap path under a permissive detector: the scan itself
// narrows to the classes the caller can act on, instead of running every
// recognizer and throwing all but two answers away in the caller's filter.
func TestScanTextForRestrictsTheScan(t *testing.T) {
	d := newDet(t, alcatraz.Options{}) // permissive: every class active
	const text = "mail ada@example.com card 4111111111111111"

	all := d.ScanText(text)
	if !slices.Contains(all, alcz.EmailAddress) || !slices.Contains(all, alcz.CreditCard) {
		t.Fatalf("ScanText = %v, want both classes present to make the narrowing visible", all)
	}

	want := []string{alcz.CreditCard}
	if got := d.ScanTextFor(want, text); !slices.Equal(got, want) {
		t.Errorf("ScanTextFor = %v, want %v: the email must not be scanned for", got, want)
	}
}

// A rule naming a class the detector does not carry narrows the scan to
// nothing. Widening it back to the active set would report classes the caller
// never asked about, which is the intersection ScanTextFor exists to skip.
func TestScanTextForOutsideTheActiveSetFindsNothing(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.CreditCard}})

	got := d.ScanTextFor([]string{alcz.EmailAddress}, "mail ada@example.com card 4111111111111111")
	if got != nil {
		t.Errorf("ScanTextFor = %v, want nil", got)
	}
}

// The ScopedScanner contract: an empty list asks for no narrowing at all.
func TestScanTextForEmptyListMatchesScanText(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.CreditCard, alcz.EmailAddress}})
	const text = "mail ada@example.com card 4111111111111111"

	if got, want := d.ScanTextFor(nil, text), d.ScanText(text); !slices.Equal(got, want) {
		t.Errorf("ScanTextFor(nil) = %v, want ScanText's answer %v", got, want)
	}
	if got := d.ScanTextFor([]string{alcz.CreditCard}, ""); got != nil {
		t.Errorf("ScanTextFor over empty text = %v", got)
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
