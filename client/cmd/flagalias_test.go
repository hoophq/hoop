package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// The approval flags and their old review names set the same value, and help
// shows only the approval names.
func TestApprovalFlagAliases(t *testing.T) {
	for _, tt := range []struct {
		name     string
		fs       *pflag.FlagSet
		flag     string
		oldFlag  string
		value    string
		read     func() string
		resetVal func()
	}{
		{"sessions status", sessionsListCmd.Flags(), "approval-status", "review-status", "pending",
			func() string { return sessionsFlags.reviewStatus }, func() { sessionsFlags.reviewStatus = "" }},
		{"sessions approver", sessionsListCmd.Flags(), "approver", "review-approver", "a@b.c",
			func() string { return sessionsFlags.reviewApprover }, func() { sessionsFlags.reviewApprover = "" }},
		{"run groups", runCmd.Flags(), "approval", "review", "dba",
			func() string { return strings.Join(runFlags.Reviewers, ",") }, func() { runFlags.Reviewers = nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, name := range []string{tt.flag, tt.oldFlag} {
				tt.resetVal()
				f := tt.fs.Lookup(name)
				if f == nil || f.Name != tt.flag {
					t.Fatalf("--%s: got flag %v, want --%s", name, f, tt.flag)
				}
				if err := tt.fs.Parse([]string{"--" + name + "=" + tt.value}); err != nil {
					t.Fatalf("--%s: %v", name, err)
				}
				if got := tt.read(); got != tt.value {
					t.Errorf("--%s set %q, want %q", name, got, tt.value)
				}
			}
			tt.resetVal()
			if usage := tt.fs.FlagUsages(); strings.Contains(usage, "--"+tt.oldFlag+" ") {
				t.Errorf("help shows the old flag --%s", tt.oldFlag)
			}
		})
	}
}
