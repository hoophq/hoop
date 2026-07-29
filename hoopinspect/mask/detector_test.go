package mask_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoopinspect/mask"
)

// fakeDetector finds fixed substrings, one entity per substring. It stands in
// for a real detector so these tests pin the SEAM's contract, not alcatraz's
// detection quality.
type fakeDetector struct {
	// find maps an entity name to the literal it locates.
	find map[string]string

	mu    sync.Mutex
	calls int
}

func (d *fakeDetector) Entities() []string {
	out := make([]string, 0, len(d.find))
	for e := range d.find {
		out = append(out, e)
	}
	return out
}

func (d *fakeDetector) Find(entity string, data []byte) [][2]int {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()

	lit, ok := d.find[entity]
	if !ok || lit == "" {
		return nil
	}
	var out [][2]int
	for off := 0; ; {
		i := strings.Index(string(data[off:]), lit)
		if i < 0 {
			return out
		}
		s := off + i
		out = append(out, [2]int{s, s + len(lit)})
		off = s + len(lit)
	}
}

func TestDetectorSuppliesSpansForItsEntity(t *testing.T) {
	d := &fakeDetector{find: map[string]string{"BR_CPF": "111.444.777-35"}}
	m, err := mask.NewWithDetector([]mask.Rule{
		{Name: "cpf", Entity: "BR_CPF", Strategy: mask.StrategyRedact},
	}, d)
	if err != nil {
		t.Fatalf("NewWithDetector: %v", err)
	}

	out, res := m.MaskString("cpf 111.444.777-35 end")
	if want := "cpf [REDACTED:BR_CPF] end"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
	if res.Count != 1 {
		t.Errorf("Count = %d, want 1", res.Count)
	}
	if len(res.Entities) != 1 || res.Entities[0] != "BR_CPF" {
		t.Errorf("Entities = %v, want [BR_CPF]", res.Entities)
	}
}

// An entity the detector does not claim, and that is not a built-in, must fail
// at construction. A masker that starts and then masks nothing is the failure
// mode this rejects.
func TestUnknownEntityStillRejectedWithDetector(t *testing.T) {
	d := &fakeDetector{find: map[string]string{"BR_CPF": "x"}}
	_, err := mask.NewWithDetector([]mask.Rule{
		{Name: "typo", Entity: "BR_CFP"},
	}, d)
	if err == nil {
		t.Fatal("want error for an entity neither built-in nor detected")
	}
	// The error must name the detector's spellings, otherwise an operator
	// cannot tell a typo from an unwired detector.
	if !strings.Contains(err.Error(), "BR_CPF") {
		t.Errorf("error should list detector entities, got: %v", err)
	}
}

// A detector claiming a built-in name wins. Wiring a detector in is explicit,
// and silently getting the eight built-in regexes instead would be a lie.
func TestDetectorOverridesBuiltin(t *testing.T) {
	// The built-in email regex would never match this; the detector does.
	d := &fakeDetector{find: map[string]string{"email": "NOT-AN-EMAIL"}}
	m, err := mask.NewWithDetector([]mask.Rule{
		{Entity: mask.EntityEmail, Strategy: mask.StrategyRedact},
	}, d)
	if err != nil {
		t.Fatalf("NewWithDetector: %v", err)
	}

	out, _ := m.MaskString("a NOT-AN-EMAIL b real@example.com c")
	if !strings.Contains(out, "[REDACTED:email]") {
		t.Errorf("detector span not masked: %q", out)
	}
	if !strings.Contains(out, "real@example.com") {
		t.Errorf("built-in regex still ran despite detector override: %q", out)
	}
}

// A per-rule Pattern is the operator's escape hatch and outranks the detector.
func TestPatternBeatsDetector(t *testing.T) {
	d := &fakeDetector{find: map[string]string{"custom": "from-detector"}}
	m, err := mask.NewWithDetector([]mask.Rule{
		{Entity: "custom", Pattern: `from-pattern`, Strategy: mask.StrategyRedact},
	}, d)
	if err != nil {
		t.Fatalf("NewWithDetector: %v", err)
	}

	out, _ := m.MaskString("from-detector and from-pattern")
	if !strings.Contains(out, "from-detector ") {
		t.Errorf("detector should not have run: %q", out)
	}
	if !strings.Contains(out, "[REDACTED:custom]") {
		t.Errorf("pattern did not mask: %q", out)
	}
}

// Rule order is precedence, and it must hold across the two span sources.
func TestDetectorAndBuiltinSharePrecedence(t *testing.T) {
	d := &fakeDetector{find: map[string]string{"wide": "ada@example.com"}}

	// Detector rule first: it claims the span, the built-in email rule sees
	// nothing left.
	m, err := mask.NewWithDetector([]mask.Rule{
		{Entity: "wide", Strategy: mask.StrategyRedact},
		{Entity: mask.EntityEmail, Strategy: mask.StrategyRedact},
	}, d)
	if err != nil {
		t.Fatalf("NewWithDetector: %v", err)
	}
	out, res := m.MaskString("mail ada@example.com")
	if !strings.Contains(out, "[REDACTED:wide]") {
		t.Errorf("first rule should claim the span: %q", out)
	}
	if res.Count != 1 {
		t.Errorf("overlapping span masked twice: Count = %d", res.Count)
	}

	// Reversed, the built-in claims it first.
	m2, err := mask.NewWithDetector([]mask.Rule{
		{Entity: mask.EntityEmail, Strategy: mask.StrategyRedact},
		{Entity: "wide", Strategy: mask.StrategyRedact},
	}, d)
	if err != nil {
		t.Fatalf("NewWithDetector: %v", err)
	}
	out2, _ := m2.MaskString("mail ada@example.com")
	if !strings.Contains(out2, "[REDACTED:email]") {
		t.Errorf("first rule should claim the span: %q", out2)
	}
}

// A detector returning garbage offsets must mask nothing, never panic. It runs
// on every response byte of a live relay; a slice out of range there takes the
// connection down.
func TestDetectorGarbageSpansAreIgnored(t *testing.T) {
	bad := &boundsDetector{}
	m, err := mask.NewWithDetector([]mask.Rule{{Entity: "bad"}}, bad)
	if err != nil {
		t.Fatalf("NewWithDetector: %v", err)
	}

	in := "hello world"
	out, res := m.MaskString(in)
	if out != in {
		t.Errorf("garbage spans changed output: %q", out)
	}
	if res.Count != 0 {
		t.Errorf("Count = %d, want 0", res.Count)
	}
}

type boundsDetector struct{}

func (boundsDetector) Entities() []string { return []string{"bad"} }
func (boundsDetector) Find(_ string, data []byte) [][2]int {
	return [][2]int{
		{-1, 3},                        // negative start
		{0, len(data) + 100},           // end past the buffer
		{5, 5},                         // empty span
		{4, 2},                         // inverted
		{len(data), len(data)},         // empty at the tail
		{len(data) + 1, len(data) + 2}, // wholly out of range
	}
}

// MaskJSON walks string leaves; a detector must reach them the same way the
// built-in regexes do.
func TestDetectorRunsUnderMaskJSON(t *testing.T) {
	d := &fakeDetector{find: map[string]string{"BR_CPF": "111.444.777-35"}}
	m, err := mask.NewWithDetector([]mask.Rule{
		{Entity: "BR_CPF", Strategy: mask.StrategyRedact},
	}, d)
	if err != nil {
		t.Fatalf("NewWithDetector: %v", err)
	}

	out, res, err := m.MaskJSON([]byte(`{"doc":"111.444.777-35","n":42}`))
	if err != nil {
		t.Fatalf("MaskJSON: %v", err)
	}
	if !strings.Contains(string(out), "[REDACTED:BR_CPF]") {
		t.Errorf("value not masked: %s", out)
	}
	if res.Count != 1 {
		t.Errorf("Count = %d, want 1", res.Count)
	}
}

// One Masker serves every connection, so the detector path must be race-free.
func TestDetectorConcurrentMasking(t *testing.T) {
	d := &fakeDetector{find: map[string]string{"BR_CPF": "111.444.777-35"}}
	m, err := mask.NewWithDetector([]mask.Rule{{Entity: "BR_CPF"}}, d)
	if err != nil {
		t.Fatalf("NewWithDetector: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, res := m.MaskString("cpf 111.444.777-35")
			if res.Count != 1 || strings.Contains(out, "111.444.777-35") {
				t.Errorf("concurrent mask wrong: %q %+v", out, res)
			}
		}()
	}
	wg.Wait()
}

// New is NewWithDetector(nil): the built-ins must behave exactly as before.
func TestNewIsDetectorlessNewWithDetector(t *testing.T) {
	rules := []mask.Rule{{Entity: mask.EntityEmail, Strategy: mask.StrategyRedact}}

	a, err := mask.New(rules)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b, err := mask.NewWithDetector(rules, nil)
	if err != nil {
		t.Fatalf("NewWithDetector: %v", err)
	}

	const in = "mail ada@example.com now"
	outA, resA := a.MaskString(in)
	outB, resB := b.MaskString(in)
	if outA != outB || resA.Count != resB.Count {
		t.Errorf("New and NewWithDetector(nil) differ: %q/%+v vs %q/%+v",
			outA, resA, outB, resB)
	}
}
