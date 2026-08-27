package alcatraz_test

import (
	"strings"
	"sync"
	"testing"

	alcz "github.com/hoophq/alcatraz/entities"
	"github.com/hoophq/hoop/hoopinspect/gate"
	"github.com/hoophq/hoop/hoopinspect/pii/alcatraz"
)

// --- strategies ------------------------------------------------------------

func TestStrategies(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule alcatraz.Rule
		in   string
		want string
	}{
		{
			"redact names the class so the reader knows what was removed",
			alcatraz.Rule{Entity: alcz.CreditCard, Strategy: alcatraz.StrategyRedact},
			"card 4111111111111111 end",
			"card [REDACTED:CREDIT_CARD] end",
		},
		{
			"mask preserves rune count for fixed-width consumers",
			alcatraz.Rule{Entity: alcz.CreditCard, Strategy: alcatraz.StrategyMask},
			"card 4111111111111111 end",
			"card **************** end",
		},
		{
			"partial keeps the tail a human recognizes",
			alcatraz.Rule{Entity: alcz.CreditCard, Strategy: alcatraz.StrategyPartial, KeepLast: 4},
			"card 4111111111111111 end",
			"card ************1111 end",
		},
		{
			"empty strategy redacts: an unconfigured rule shows less, not more",
			alcatraz.Rule{Entity: alcz.CreditCard},
			"card 4111111111111111 end",
			"card [REDACTED:CREDIT_CARD] end",
		},
		{
			"custom mask char",
			alcatraz.Rule{Entity: alcz.CreditCard, Strategy: alcatraz.StrategyMask, MaskChar: '#'},
			"card 4111111111111111 end",
			"card ################ end",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newDet(t, alcatraz.Options{Entities: []string{tc.rule.Entity}})
			got, res := newMask(t, d, tc.rule).MaskString(tc.in)
			if got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
			if res.Count != 1 {
				t.Errorf("Count = %d, want 1", res.Count)
			}
		})
	}
}

// Separators are format, not data. Keeping them leaves the output legible as
// a card number rather than a corrupted string, and legible output stops the
// support ticket.
func TestPartialKeepsSeparators(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.CreditCard}})
	m := newMask(t, d, alcatraz.Rule{
		Entity: alcz.CreditCard, Strategy: alcatraz.StrategyPartial, KeepLast: 4,
	})

	got, _ := m.MaskString("4111-1111-1111-1111")
	if want := "****-****-****-1111"; got != want {
		t.Errorf("got %q, want %q; dashes must survive", got, want)
	}
}

// KeepLast at or above the value length masks nothing. Fail towards the safe
// end.
func TestPartialKeepLastTooLargeMasksEverything(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.CreditCard}})
	m := newMask(t, d, alcatraz.Rule{
		Entity: alcz.CreditCard, Strategy: alcatraz.StrategyPartial, KeepLast: 99,
	})

	got, _ := m.MaskString("4111111111111111")
	if strings.Contains(got, "4111") {
		t.Errorf("value survived an oversized keep_last: %q", got)
	}
}

// Hash is deterministic so a masked column still works as a join key.
func TestHashIsStableAndOpaque(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.EmailAddress}})
	m := newMask(t, d, alcatraz.Rule{Entity: alcz.EmailAddress, Strategy: alcatraz.StrategyHash})

	a, _ := m.MaskString("ada@example.com")
	b, _ := m.MaskString("ada@example.com")
	c, _ := m.MaskString("grace@example.com")

	if a != b {
		t.Errorf("same input gave different output: %q vs %q", a, b)
	}
	if a == c {
		t.Error("different inputs collided")
	}
	if strings.Contains(a, "ada@example.com") {
		t.Errorf("value survived hashing: %q", a)
	}
	if !strings.Contains(a, "sha256:") {
		t.Errorf("expected a sha256: prefix, got %q", a)
	}
}

// --- rule validation -------------------------------------------------------

// Every invalid rule is reported at once: finding out about the second typo
// on the next restart is how a rollout takes three rounds instead of one.
func TestAllInvalidRulesReportedTogether(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.USSSN, alcz.CreditCard}})

	_, err := alcatraz.NewMasker(d, []alcatraz.Rule{
		{Name: "a", Entity: alcz.USSSN, Strategy: "nonsense"},
		{Name: "b", Entity: alcz.CreditCard, KeepLast: -1},
		{Name: "c", Entity: ""},
	})
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"a:", "b:", "c:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name rule %s: %v", want, err)
		}
	}
}

// A rule naming an entity the detector was not configured for would silently
// mask nothing, which looks exactly like working masking.
func TestRuleForUndetectedEntityRejected(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.USSSN}})

	_, err := alcatraz.NewMasker(d, []alcatraz.Rule{{Name: "r", Entity: alcz.BRCPF}})
	if err == nil {
		t.Fatal("want an error for an entity outside the detector's set")
	}
	if !strings.Contains(err.Error(), alcz.BRCPF) || !strings.Contains(err.Error(), alcz.USSSN) {
		t.Errorf("error should name both the bad entity and the configured set: %v", err)
	}
}

// Two rules for one entity is ambiguous: the second would silently replace
// the first.
func TestDuplicateEntityRejected(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.USSSN}})

	_, err := alcatraz.NewMasker(d, []alcatraz.Rule{
		{Name: "first", Entity: alcz.USSSN, Strategy: alcatraz.StrategyRedact},
		{Name: "second", Entity: alcz.USSSN, Strategy: alcatraz.StrategyHash},
	})
	if err == nil {
		t.Fatal("want an error for two rules on one entity")
	}
	if !strings.Contains(err.Error(), "first") {
		t.Errorf("error should name the rule already holding the entity: %v", err)
	}
}

// --- Mask behaviour --------------------------------------------------------

// A clean payload must cost nothing and be returned as-is, not copied.
func TestCleanPayloadReturnedUnchanged(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.USSSN}})
	m := newMask(t, d, alcatraz.Rule{Entity: alcz.USSSN})

	in := []byte("SELECT name FROM customers WHERE id = 1")
	out, res := m.Mask(in)
	if res.Count != 0 {
		t.Errorf("Count = %d on a clean payload", res.Count)
	}
	if &out[0] != &in[0] {
		t.Error("clean payload should be returned as-is, not copied")
	}
}

// Result names classes and counts. It must never carry the values, because
// the audit sink writes it.
func TestResultCarriesNoValues(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.EmailAddress, alcz.CreditCard}})
	m := newMask(t, d,
		alcatraz.Rule{Entity: alcz.EmailAddress},
		alcatraz.Rule{Entity: alcz.CreditCard})

	_, res := m.MaskString("ada@example.com and 4111111111111111")
	if res.Count != 2 {
		t.Errorf("Count = %d, want 2", res.Count)
	}
	for _, e := range res.Entities {
		if strings.Contains(e, "@") || strings.Contains(e, "4111") {
			t.Errorf("Result leaked a value: %q", e)
		}
	}
	// Sorted, so a log query can rely on the order.
	if len(res.Entities) != 2 || res.Entities[0] != alcz.CreditCard {
		t.Errorf("Entities = %v, want sorted [CREDIT_CARD EMAIL_ADDRESS]", res.Entities)
	}
}

// Only entities with a rule are scanned for: an unused recognizer costs
// nothing, and an unruled entity is never rewritten.
func TestOnlyRuledEntitiesAreMasked(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.EmailAddress, alcz.CreditCard}})
	m := newMask(t, d, alcatraz.Rule{Entity: alcz.EmailAddress})

	out, res := m.MaskString("ada@example.com and 4111111111111111")
	if !strings.Contains(out, "4111111111111111") {
		t.Errorf("an entity with no rule was masked: %q", out)
	}
	if res.Count != 1 {
		t.Errorf("Count = %d, want 1", res.Count)
	}
}

// Byte offsets, not rune offsets: a multi-byte prefix must not shift the span.
func TestMultiByteTextMaskedCorrectly(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.EmailAddress}})
	m := newMask(t, d, alcatraz.Rule{Entity: alcz.EmailAddress})

	out, res := m.MaskString("héllo — wörld ada@example.com")
	if res.Count != 1 {
		t.Fatalf("Count = %d, want 1", res.Count)
	}
	if !strings.HasPrefix(out, "héllo — wörld ") {
		t.Errorf("multi-byte prefix corrupted: %q", out)
	}
	if strings.Contains(out, "ada@example.com") {
		t.Errorf("email survived: %q", out)
	}
}

// --- secrets ---------------------------------------------------------------

// Alcatraz is a PII engine and detects none of these on its own. They are the
// reason secrets.go exists: a response body carrying a credential is a worse
// leak than one carrying a phone number.
func TestSecretRecognizers(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{
		alcatraz.AWSAccessKey, alcatraz.JWT, alcatraz.PrivateKey,
	}})
	m := newMask(t, d,
		alcatraz.Rule{Entity: alcatraz.AWSAccessKey},
		alcatraz.Rule{Entity: alcatraz.JWT},
		alcatraz.Rule{Entity: alcatraz.PrivateKey})

	for _, tc := range []struct{ name, in, leak string }{
		{"AWS access key", "key AKIAIOSFODNN7EXAMPLE here", "AKIAIOSFODNN7EXAMPLE"},
		{"AWS session key", "key ASIAIOSFODNN7EXAMPLE here", "ASIAIOSFODNN7EXAMPLE"},
		{
			"JWT",
			"tok eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk end",
			"eyJhbGciOiJIUzI1NiJ9",
		},
		{
			"PEM private key",
			"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA1234\n-----END RSA PRIVATE KEY-----",
			"MIIEowIBAAKCAQEA1234",
		},
	} {
		out, res := m.MaskString(tc.in)
		if res.Count == 0 {
			t.Errorf("%s: not detected", tc.name)
			continue
		}
		if strings.Contains(out, tc.leak) {
			t.Errorf("%s: secret survived masking: %q", tc.name, out)
		}
	}
}

// The JWT validator decodes the header: a dotted path or hostname must not be
// masked as a token.
func TestJWTValidatorRejectsLookalikes(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcatraz.JWT}})
	m := newMask(t, d, alcatraz.Rule{Entity: alcatraz.JWT})

	for _, in := range []string{
		"eyJhbGc.notbase64!!.x",
		"eyJxxxx.eyJyyyy.zzzz", // decodes but has no alg claim
	} {
		out, res := m.MaskString(in)
		if res.Count != 0 {
			t.Errorf("%q was masked as a JWT: %q", in, out)
		}
	}
}

// The truncated-PEM fallback must still eat the key material. Emitting a
// placeholder directly above the bytes it claimed to remove is the failure.
func TestTruncatedPrivateKeyStillMasksMaterial(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcatraz.PrivateKey}})
	m := newMask(t, d, alcatraz.Rule{Entity: alcatraz.PrivateKey})

	out, res := m.MaskString("-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA1234\n")
	if res.Count == 0 {
		t.Fatal("truncated PEM block not detected")
	}
	if strings.Contains(out, "MIIEowIBAAKCAQEA1234") {
		t.Errorf("key material survived: %q", out)
	}
}

// --- plugin contract -------------------------------------------------------

// BuildMasker is how the sidecar gets a masker without linking this module.
func TestBuildMaskerFromJSON(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.BRCPF, alcz.EmailAddress}})

	var _ gate.Masker // the contract being satisfied
	m, err := d.BuildMasker([]byte(`[
		{"name":"cpf","entity":"BR_CPF","strategy":"redact"},
		{"name":"mail","entity":"EMAIL_ADDRESS","strategy":"redact"}
	]`))
	if err != nil {
		t.Fatalf("BuildMasker: %v", err)
	}
	if m == nil {
		t.Fatal("want a masker")
	}

	out, entities, count := m.Mask([]byte("cpf 111.444.777-35 mail ada@example.com"))
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if strings.Contains(string(out), "111.444.777-35") {
		t.Errorf("CPF survived: %s", out)
	}
	if len(entities) != 2 {
		t.Errorf("entities = %v", entities)
	}
}

// A typo in a config key must not silently disable a masking rule.
func TestBuildMaskerRejectsUnknownField(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.BRCPF}})

	if _, err := d.BuildMasker([]byte(`[{"entity":"BR_CPF","stratergy":"redact"}]`)); err == nil {
		t.Fatal("a misspelled field was accepted")
	}
}

func TestBuildMaskerEmptyRules(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.BRCPF}})

	for _, raw := range [][]byte{nil, []byte(`[]`)} {
		m, err := d.BuildMasker(raw)
		if err != nil {
			t.Errorf("%q: %v", raw, err)
		}
		if m != nil {
			t.Errorf("%q: want a nil masker for empty rules", raw)
		}
	}
}

// One Masker serves every connection.
func TestMaskerConcurrent(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.CreditCard, alcz.BRCPF}})
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
		}()
	}
	wg.Wait()
}
