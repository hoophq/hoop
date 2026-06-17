package cmd

import (
	"bytes"
	"strings"
	"testing"

	clientupgrade "github.com/hoophq/hoop/client/upgrade"
)

func TestPrintVersionMismatchWarning(t *testing.T) {
	const installLine = "hoop versions install"
	const docsLine = "https://hoop.dev/docs/clients/cli-versions"

	tests := []struct {
		name         string
		cliVersion   string
		agentVersion string
		disableEnv   string
		wantOutput   bool
		wantContains []string
		wantMissing  []string
	}{
		{
			name:         "versions match - no warning",
			cliVersion:   "1.87.2",
			agentVersion: "1.87.2",
			wantOutput:   false,
		},
		{
			name:         "cli version unknown - no warning",
			cliVersion:   "unknown",
			agentVersion: "1.87.2",
			wantOutput:   false,
		},
		{
			name:         "empty agent version - no warning",
			cliVersion:   "1.87.2",
			agentVersion: "",
			wantOutput:   false,
		},
		{
			name:         "disabled via env - no warning",
			cliVersion:   "1.86.0",
			agentVersion: "1.87.2",
			disableEnv:   "true",
			wantOutput:   false,
		},
		{
			name:         "installable agent version recommends version manager",
			cliVersion:   "1.86.0",
			agentVersion: "1.87.2",
			wantOutput:   true,
			wantContains: []string{"1.86.0", "1.87.2", installLine + " 1.87.2 --use", clientupgrade.DisableVersionCheckEnv},
		},
		{
			name:         "leading v on agent version is normalized in the command",
			cliVersion:   "1.86.0",
			agentVersion: "v1.87.2",
			wantOutput:   true,
			wantContains: []string{installLine + " 1.87.2 --use"},
			wantMissing:  []string{installLine + " v1.87.2"},
		},
		{
			name:         "below-floor agent version falls back to docs",
			cliVersion:   "1.87.2",
			agentVersion: "1.70.0",
			wantOutput:   true,
			wantContains: []string{"1.70.0", docsLine},
			wantMissing:  []string{installLine},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set the env explicitly so the test is hermetic regardless of
			// the surrounding shell. t.Setenv restores it on cleanup.
			t.Setenv(clientupgrade.DisableVersionCheckEnv, tt.disableEnv)

			var buf bytes.Buffer
			printVersionMismatchWarningTo(&buf, tt.cliVersion, tt.agentVersion)
			out := buf.String()

			if !tt.wantOutput {
				if out != "" {
					t.Fatalf("expected no output, got %q", out)
				}
				return
			}
			if out == "" {
				t.Fatalf("expected a warning, got empty output")
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("expected output to contain %q, got %q", want, out)
				}
			}
			for _, missing := range tt.wantMissing {
				if strings.Contains(out, missing) {
					t.Errorf("expected output NOT to contain %q, got %q", missing, out)
				}
			}
		})
	}
}
