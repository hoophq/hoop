package alcatraz_test

import (
	"strings"
	"testing"

	alcz "github.com/hoophq/alcatraz/entities"
	"github.com/hoophq/hoop/sidecar/pii/alcatraz"
)

// RedactText backs the analyzer's send: redacted mode, whose contract is
// that no detected VALUE leaves the process. The value must be gone from
// the rewrite, replaced by its class, with the classes named beside it.
func TestRedactTextRemovesValues(t *testing.T) {
	d := newDet(t, alcatraz.Options{
		Entities: []string{alcz.CreditCard, alcz.EmailAddress},
	})
	const card, mail = "4111111111111111", "ada@example.com"

	out, names := d.RedactText("card " + card + " mail " + mail)

	if strings.Contains(out, card) || strings.Contains(out, mail) {
		t.Fatalf("a detected value survived the rewrite: %q", out)
	}
	if !strings.Contains(out, "<"+alcz.CreditCard+">") ||
		!strings.Contains(out, "<"+alcz.EmailAddress+">") {
		t.Errorf("classes did not replace the values: %q", out)
	}
	want := []string{alcz.CreditCard, alcz.EmailAddress}
	if len(names) != 2 || names[0] != want[0] || names[1] != want[1] {
		t.Errorf("names = %v, want %v sorted", names, want)
	}
}

// Clean text comes back untouched with no names: the common case costs one
// scan and no rewrite.
func TestRedactTextCleanAndEmpty(t *testing.T) {
	d := newDet(t, alcatraz.Options{Entities: []string{alcz.CreditCard}})

	const clean = "SELECT id FROM orders WHERE status = 'open'"
	if out, names := d.RedactText(clean); out != clean || names != nil {
		t.Errorf("clean text changed: %q, names %v", out, names)
	}
	if out, names := d.RedactText(""); out != "" || names != nil {
		t.Errorf("empty text changed: %q, names %v", out, names)
	}
}

// A value two recognizers claim is removed whole: overlapping spans
// collapse onto one replacement, and no tail of the value survives.
func TestRedactTextOverlappingSpans(t *testing.T) {
	d := newDet(t, alcatraz.Options{}) // permissive: every class active
	const card = "4111111111111111"

	out, _ := d.RedactText("pay with " + card + " today")

	if strings.Contains(out, card) {
		t.Fatalf("the value survived overlapping detections: %q", out)
	}
	// However many classes claimed it, the digits are gone.
	for i := 0; i+8 <= len(card); i++ {
		if strings.Contains(out, card[i:i+8]) {
			t.Fatalf("a fragment of the value survived: %q", out)
		}
	}
}
