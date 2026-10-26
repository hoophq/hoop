package agent

import (
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
