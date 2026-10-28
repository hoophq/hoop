package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hoophq/alcatraz/ner"
)

// ALCATRAZ_NER_BACKEND is a startup contract: empty takes the build default,
// a known name wins, anything else stops the agent before the first session.
func TestParseNerBackend(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", want: defaultNerBackend},
		{in: ner.BackendORT, want: ner.BackendORT},
		{in: ner.BackendGo, want: ner.BackendGo},
		{in: "ORT", wantErr: true},
		{in: "xla", wantErr: true},
		{in: "onnx", wantErr: true},
	}
	for _, tc := range tests {
		got, err := parseNerBackend(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseNerBackend(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseNerBackend(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseNerBackend(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The build default must be one of the two names the override accepts.
func TestDefaultNerBackendIsAccepted(t *testing.T) {
	if defaultNerBackend != ner.BackendGo && defaultNerBackend != ner.BackendORT {
		t.Fatalf("defaultNerBackend = %q, want %q or %q", defaultNerBackend, ner.BackendGo, ner.BackendORT)
	}
	if ortCompiledIn != (defaultNerBackend == ner.BackendORT) {
		t.Fatalf("ortCompiledIn = %v with defaultNerBackend = %q", ortCompiledIn, defaultNerBackend)
	}
}

// The startup check must answer what the ORT backend will do at load: an
// explicit path wins and must exist (file or directory), otherwise the probe
// directories are searched in order.
func TestResolveORTLibrary(t *testing.T) {
	withLib := t.TempDir()
	lib := filepath.Join(withLib, ortLibraryName)
	if err := os.WriteFile(lib, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()

	tests := []struct {
		name     string
		explicit string
		probe    []string
		want     string
		wantErr  bool
	}{
		{name: "explicit file", explicit: lib, want: lib},
		{name: "explicit dir", explicit: withLib, want: lib},
		{name: "explicit missing", explicit: filepath.Join(empty, "nope.so"), wantErr: true},
		{name: "explicit dir without library", explicit: empty, wantErr: true},
		{name: "probe finds it", probe: []string{empty, withLib}, want: lib},
		{name: "probe misses", probe: []string{empty}, wantErr: true},
		{name: "nothing to probe", wantErr: true},
	}
	for _, tc := range tests {
		got, err := resolveORTLibrary(tc.explicit, tc.probe)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: got %q, want error", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
