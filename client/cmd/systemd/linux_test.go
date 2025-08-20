package systemd

import (
	"strings"
	"testing"
)

type fakeRunner struct {
	calls [][]string
	out   string
	err   error
}

func (f *fakeRunner) Run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.out, f.err
}

func (f *fakeRunner) Logs(name string, args ...string) error {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.err
}

func TestDerivePaths_UserMode_Defaults(t *testing.T) {
	up := userPaths(Options{ServiceName: "hoopd"})
	if !strings.HasSuffix(up, "/.config/systemd/user/hoopd.service") {
		t.Fatalf("unit path=%q, want ~/.config/systemd/user/hoopd.service", up)
	}
}

func TestRenderUnit_IncludesEnvAndExec(t *testing.T) {
	unit := renderServiceFile(unitData{
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
