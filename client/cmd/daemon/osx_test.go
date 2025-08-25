package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStopDarwinAgent_CallsBootout(t *testing.T) {
	mr := &mockRunner{}
	orig := execRunner
	execRunner = mr
	t.Cleanup(func() { execRunner = orig })

	if err := stopDarwin("hoop-agent"); err != nil {
		t.Fatalf("stopDarwin error: %v", err)
	}

	if len(mr.callsRun) != 1 {
		t.Fatalf("expected 1 run call, got %d", len(mr.callsRun))
	}
	guiTarget := currentGuiTarget()
	got := mr.callsRun[0]
	want := fmt.Sprintf("launchctl bootout %s/hoop-agent", guiTarget)
	if got != string(want) {
		t.Fatalf("bootout call = %v; want %v", got, want)
	}
}

func TestRenderLaunchAgentPlist_XMLAndBooleans(t *testing.T) {
	d := launchAgentData{
		Label:                 "com.example.app",
		Program:               "/path/with space/bin",
		ProgramArgumentsExtra: []string{"start", "--name=foo&bar"},
		EnvironmentVariables:  map[string]string{"K": `v<&>"'`},
		RunAtLoad:             true,
		KeepAlive:             false,
		StandardOutPath:       "/tmp/out.log",
		StandardErrorPath:     "/tmp/err.log",
	}

	xml := renderLaunchAgentPlist(d)
	if !strings.Contains(xml, "<key>RunAtLoad</key>\n\t<true/>") {
		t.Fatalf("RunAtLoad true not present:\n%s", xml)
	}
	if !strings.Contains(xml, "<key>KeepAlive</key>\n\t<false/>") {
		t.Fatalf("KeepAlive false not present:\n%s", xml)
	}

	// xml escaping
	if !strings.Contains(xml, xmlEscape(`/path/with space/bin`)) {
		t.Fatalf("Program not escaped/present")
	}
	if !strings.Contains(xml, "<string>--name=foo&amp;bar</string>") {
		t.Fatalf("arg not xml-escaped")
	}

	if !strings.Contains(xml, "<key>K</key>\n\t\t<string>v&lt;&amp;&gt;&quot;&apos;</string>") {
		t.Fatalf("env value not xml-escaped:\n%s", xml)
	}

}

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{"start agent", []string{"start", "agent"}},
		{"  start   agent --flag=1  ", []string{"start", "agent", "--flag=1"}},
	}

	for _, tc := range tests {
		got := splitArgs(tc.in)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("splitArgs(%q) = %#v; want %#v", tc.in, got, tc.want)
		}
	}
}

func TestLogsDarwinAgent_TailsFixedPathsAndEnsuresFiles(t *testing.T) {
	restore, fakeHome := withTempHome(t)
	defer restore()

	stdOut := filepath.Join(fakeHome, "Library", "Logs", "hoop-agent.out.log")
	stdErr := filepath.Join(fakeHome, "Library", "Logs", "hoop-agent.err.log")

	mr := &mockRunner{}
	orig := execRunner
	execRunner = mr
	t.Cleanup(func() { execRunner = orig })

	if err := LogsDarwinAgent(); err != nil {
		t.Fatalf("LogsDarwinAgent error: %v", err)
	}

	if _, err := os.Stat(stdOut); err != nil {
		t.Fatalf("stdout log not created: %v", err)
	}
	if _, err := os.Stat(stdErr); err != nil {
		t.Fatalf("stderr log not created: %v", err)
	}

	if len(mr.callsLog) != 1 {
		t.Fatalf("expected 1 Logs call, got %d", len(mr.callsLog))
	}

	got := strings.Fields(mr.callsLog[0])
	if len(got) < 6 || got[0] != "tail" || got[1] != "-n" || got[2] != "+1" || got[3] != "-F" {
		t.Fatalf("unexpected tail invocation: %v", got)
	}
	if got[4] != stdOut || got[5] != stdErr {
		t.Fatalf("tail args logs mismatch: %v; want %v and %v", got, stdOut, stdErr)
	}
}


func TestRemoveDarwinAgent_StopsAndRemovesPlist(t *testing.T) {
	restore, _ := withTempHome(t)
	defer restore()

	opts := Options{ServiceName: "hoop-agent"}
	plistPath, err := userLaunchAgentPath(opts)
	if err != nil {
		t.Fatalf("userLaunchAgentPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(plistPath, []byte("dummy"), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}

	mr := &mockRunner{}
	orig := execRunner
	execRunner = mr
	t.Cleanup(func() { execRunner = orig })

	if err := removeDarwin("hoop-agent"); err != nil {
		t.Fatalf("removeDarwin error: %v", err)
	}

	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("plist should be removed, stat err: %v", err)
	}
	if len(mr.callsRun) == 0 {
		t.Fatalf("expected bootout call before removal")
	}
}
