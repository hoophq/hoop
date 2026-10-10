package conformance

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func run(t *testing.T, module string, fixtures ...[]byte) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := Run(context.Background(), testdata(t, module), fixtures, &out)
	t.Log(out.String())
	return out.String(), err
}

func TestAcmewireFixturesPass(t *testing.T) {
	out, err := run(t, "acmewire.wasm", testdata(t, "acmewire.fixtures.json"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, n := range []string{"1", "2", "3", "4", "5", "6", "7", "8"} {
		if !strings.Contains(out, "\n"+n+"     ok") {
			t.Fatalf("check %s is not reported ok", n)
		}
	}
}

func TestWrongExpectationsAreReported(t *testing.T) {
	// A Q frame: the whole frame consumed, one select with verb QUERY.
	fixture := []byte(`[
	  {"name": "wrong consumed", "steps": [
	    {"dir": "client", "hex": "510000000853454c4543542031", "expect": {"consumed": 99}}]},
	  {"name": "wrong field", "steps": [
	    {"dir": "client", "hex": "510000000853454c4543542031",
	     "expect": {"statements": [{"operation": "select", "metadata": {"x-acmewire.verb": "PURGE"}}]}}]},
	  {"name": "expected error that does not happen", "steps": [
	    {"dir": "client", "hex": "510000000853454c4543542031", "expect": {"error": true}}]},
	  {"name": "steps after an error", "steps": [
	    {"dir": "client", "hex": "5800000000", "expect": {"error": true}},
	    {"dir": "client", "hex": "510000000853454c4543542031"}]}
	]`)
	out, err := run(t, "acmewire.wasm", fixture)
	if err == nil || !errors.Is(err, ErrFailed) || !strings.HasSuffix(err.Error(), ": 6") {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{
		`script "wrong consumed" step 1: consumed 13, want 99`,
		`script "wrong field" step 1: statements[0].metadata.x-acmewire.verb does not match`,
		`script "expected error that does not happen" step 1: expected a decode error, got none`,
		`script "steps after an error" step 1: a decode error ends the connection, but 1 step(s) follow`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report lacks %q", want)
		}
	}
	// The other scripts' results still feed checks 7 and 8.
	if !strings.Contains(out, "\n7     ok") || !strings.Contains(out, "\n8     ok") {
		t.Fatal("checks 7 and 8 did not run on the scripts that decoded")
	}
}

func TestUnknownFixtureKeyIsRefused(t *testing.T) {
	_, err := run(t, "acmewire.wasm", []byte(`[{"name": "x", "steps": [{"dir": "client", "hex": "00", "expects": {}}]}]`))
	if err == nil || !strings.HasSuffix(err.Error(), ": 6") {
		t.Fatalf("Run: %v", err)
	}
}

func TestGenericChecksAloneWithoutFixtures(t *testing.T) {
	out, err := run(t, "cfixture.wasm")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "\n6     skip   no fixtures") || !strings.Contains(out, "\n3     ok") {
		t.Fatalf("report: %s", out)
	}
}

func TestTrappingDecodeFailsCheck2AndSurfacesInCheck5(t *testing.T) {
	out, err := run(t, "cfixture_trap.wasm")
	if err == nil || !strings.HasSuffix(err.Error(), ": 2") {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "\n2     FAIL   decode of zero bytes from client") {
		t.Fatalf("check 2: %s", out)
	}
	if !strings.Contains(out, "\n5     ok     garbage input returned without a hang or a panic; client: error \"") || !strings.Contains(out, "unreachable") {
		t.Fatalf("check 5: %s", out)
	}
}

func TestSpinningDecodeIsStoppedByTheDeadline(t *testing.T) {
	out, err := run(t, "cfixture_spin.wasm")
	if err == nil || !strings.HasSuffix(err.Error(), ": 2") {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "exceeded call_timeout_ms (200ms)") {
		t.Fatalf("report: %s", out)
	}
}

func TestBrokenModuleFailsCheck1(t *testing.T) {
	_, err := run(t, "cfixture_badabi.wasm")
	if err == nil || !strings.HasSuffix(err.Error(), ": 1") {
		t.Fatalf("Run: %v", err)
	}
}

func TestSubsetMatching(t *testing.T) {
	want := map[string]any{"a": float64(1), "m": map[string]any{"k": "v"}, "l": []any{float64(1), map[string]any{"x": "y"}}}
	got := map[string]any{"a": float64(1), "b": "extra", "m": map[string]any{"k": "v", "k2": "v2"}, "l": []any{float64(1), map[string]any{"x": "y", "z": "w"}}}
	if path, ok := subset(want, got, "s"); !ok {
		t.Fatalf("subset refused a superset at %s", path)
	}
	got["l"].([]any)[1].(map[string]any)["x"] = "nope"
	if path, ok := subset(want, got, "s"); ok || path != "s.l[1].x" {
		t.Fatalf("mismatch path %q ok %v", path, ok)
	}
	if path, ok := subset([]any{float64(1)}, []any{float64(1), float64(2)}, "s"); ok || path != "s" {
		t.Fatalf("array length: %q %v", path, ok)
	}
}
