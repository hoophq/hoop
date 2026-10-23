package controller

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/libhoop"

	pb "github.com/hoophq/hoop/common/proto"
)

// TestDBExecRefusesPsqlEscape runs the driver exec of the libhoop this build
// links. The workspace picks the newest libhoop any module requires, so a bump
// elsewhere can link a libhoop without the hoophq/hoop#1548 fix, as 1.258.1
// did. That libhoop sends "\!" to the server instead of refusing it before any
// connection.
func TestDBExecRefusesPsqlEscape(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd, err := libhoop.NewAdHocDBExec("postgres", []byte("\\! echo escaped\nSELECT 1;"), &stdout, &stderr,
		map[string]string{"sid": "terminal-exec-test"})
	if err != nil {
		t.Fatalf("NewAdHocDBExec: %v", err)
	}
	done := make(chan int, 1)
	cmd.Run(func(exitCode int, _ string) { done <- exitCode })
	select {
	case code := <-done:
		if code != 3 || !strings.Contains(stderr.String(), `\!: meta-command not supported by driver exec`) {
			t.Errorf("exit=%d stderr=%q, want exit 3 and the \\! refusal", code, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("driver exec did not exit")
	}
}

func TestDBExecResultMetadata(t *testing.T) {
	tests := []struct {
		name           string
		resultMetadata string
		clientVerb     string
		want           bool
	}{
		{"exec keeps the connection default", "on", pb.ClientVerbExec, true},
		{"exec honors RESULT_METADATA=off", "off", pb.ClientVerbExec, false},
		{"plain exec gets batch output", "on", pb.ClientVerbPlainExec, false},
		{"plain exec with RESULT_METADATA=off", "off", pb.ClientVerbPlainExec, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dbExecResultMetadata(&connEnv{resultMetadata: tt.resultMetadata}, tt.clientVerb)
			if got != tt.want {
				t.Errorf("dbExecResultMetadata(%q, %q) = %v, want %v", tt.resultMetadata, tt.clientVerb, got, tt.want)
			}
		})
	}
}
