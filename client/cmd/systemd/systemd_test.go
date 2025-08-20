package systemd

import (
	"strings"
	"testing"
)

func TestRenderUnit_IncludesEnvAndExec(t *testing.T) {
	unit := renderUnit(unitData{
		Description: "Hoop Service",
		ExecPath:    "/usr/bin/hoop",
		ExecArgs:    " --flag=1",
		Env: map[string]string{
			"FOO": "bar",
			"BAZ": "qux",
		},
		WantedBy: "multi-user.target",
	})

	wantContains := []string{
		"Description=Hoop Service",
		"[Service]",
		`ExecStart=/usr/bin/hoop --flag=1`,
		`Environment="FOO=bar"`,
		`Environment="BAZ=qux"`,
		"[Install]",
		"WantedBy=multi-user.target",
	}

	for _, s := range wantContains {
		if !strings.Contains(unit, s) {
			t.Fatalf("rendered unit missing %q\n--- got ---\n%s", s, unit)
		}
	}
}

func TestDerivePaths_SystemMode_Defaults(t *testing.T) {
	up, wb, ctl := derivePaths(Options{ServiceName: "hoopd", UserMode: false})
	if wb != "multi-user.target" {
		t.Fatalf("WantedBy=%q, want multi-user.target", wb)
	}
	if !strings.HasSuffix(up, "/etc/systemd/system/hoopd.service") {
		t.Fatalf("unit path=%q, want /etc/systemd/system/hoopd.service", up)
	}
	if len(ctl) != 0 {
		t.Fatalf("ctl args=%v, want none", ctl)
	}
}

func TestDerivePaths_UserMode_Defaults(t *testing.T) {
	up, wb, ctl := derivePaths(Options{ServiceName: "hoopd", UserMode: true})
	if wb != "default.target" {
		t.Fatalf("WantedBy=%q, want default.target", wb)
	}
	if !strings.HasSuffix(up, "/.config/systemd/user/hoopd.service") {
		t.Fatalf("unit path=%q, want ~/.config/systemd/user/hoopd.service", up)
	}
	if len(ctl) != 1 || ctl[0] != "--user" {
		t.Fatalf("ctl args=%v, want [--user]", ctl)
	}
}

type fakeRunner struct {
	calls [][]string
	out   string
	err   error
}

func (f *fakeRunner) Run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.out, f.err
}

func TestReload_CallsSystemctl(t *testing.T) {
	origRunner, origLinux, origLookup := execRunner, isLinux, lookupSystemd
	defer func() { execRunner, isLinux, lookupSystemd = origRunner, origLinux, origLookup }()

	execRunner = &fakeRunner{}
	isLinux = func() bool { return true }
	lookupSystemd = func() error { return nil }

	if err := Reload("hoopd.service", true); err != nil {
		t.Fatal(err)
	}

	fr := execRunner.(*fakeRunner)
	got := fr.calls
	if len(got) != 2 {
		t.Fatalf("calls=%v, want 2 calls", got)
	}
	if strings.Join(got[0], " ") != "systemctl --user daemon-reload" {
		t.Fatalf("first call=%v", got[0])
	}
	if strings.Join(got[1], " ") != "systemctl --user restart hoopd.service" {
		t.Fatalf("second call=%v", got[1])
	}
}

func TestInstall_ValidatesInputs(t *testing.T) {
	origRunner, origLinux, origLookup := execRunner, isLinux, lookupSystemd
	defer func() { execRunner, isLinux, lookupSystemd = origRunner, origLinux, origLookup }()

	execRunner = &fakeRunner{}
	isLinux = func() bool { return true }
	lookupSystemd = func() error { return nil }

	err := Install(Options{})
	if err == nil || !strings.Contains(err.Error(), "service name is required") {
		t.Fatalf("got %v, want service name error", err)
	}
	err = Install(Options{ServiceName: "x"})
	if err == nil || !strings.Contains(err.Error(), "ExecPath is required") {
		t.Fatalf("got %v, want exec path error", err)
	}
}
