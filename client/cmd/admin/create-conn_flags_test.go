package admin

import (
	"strings"
	"testing"
)

// --reviewers still sets the approver groups; help shows only --approvers.
func TestCreateConnApproversFlagAlias(t *testing.T) {
	fs := createConnectionCmd.Flags()
	defer func() { reviewersFlag = nil }()
	for _, name := range []string{"approvers", "reviewers"} {
		reviewersFlag = nil
		if err := fs.Parse([]string{"--" + name + "=dba,sre"}); err != nil {
			t.Fatalf("--%s: %v", name, err)
		}
		if got := strings.Join(reviewersFlag, ","); got != "dba,sre" {
			t.Errorf("--%s set %q, want %q", name, got, "dba,sre")
		}
	}
	if usage := fs.FlagUsages(); strings.Contains(usage, "--reviewers ") || !strings.Contains(usage, "--approvers ") {
		t.Errorf("help must show --approvers and not --reviewers:\n%s", usage)
	}
}
