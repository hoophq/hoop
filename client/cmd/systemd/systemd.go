package systemd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Options struct {
	ServiceName string
	ExecPath    string
	ExecArgs    string
	Env         map[string]string
	UserMode    bool
	UnitPath    string
	WantedBy    string
}

func Install(opts Options) error {
	if err := ensureSupported(); err != nil {
		return err
	}

	if opts.ServiceName == "" {
		return errors.New("Systemd service name is required")
	}

	if opts.ExecPath == "" {
		return errors.New("Systemd ExecPath is required")
	}

	exe := opts.ExecPath
	var err error
	exe, err = filepath.Abs(exe)
	if err != nil {
		return fmt.Errorf("resolve executable symlinks: %w", err)
	}
	exe = strings.ReplaceAll(exe, "%", "%%")
	unitPath, wantedBy, ctlArgs := derivePaths(opts)
	unit := renderUnit(unitData{
		Description: fmt.Sprintf("%s Service", opts.ServiceName),
		ExecPath:    exe,
		ExecArgs:    opts.ExecArgs,
		Env:         opts.Env,
		WantedBy:    wantedBy,
		UserMode:    opts.UserMode,
	})

	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("mkdir unit dir: %w", err)
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}

	// daemon-reload
	if out, err := run("systemctl", append(ctlArgs, "daemon-reload")...); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %v\n%s", err, out)
	}

	// enable --now <service>
	if out, err := run("systemctl", append(ctlArgs, "enable", "--now", opts.ServiceName)...); err != nil {
		return fmt.Errorf("systemctl enable --now %s failed: %v\n%s", opts.ServiceName, err, out)
	}

	return nil
}

func Remove(serviceName string, userMode bool) error {
	if err := ensureSupported(); err != nil {
		return err
	}
	if serviceName == "" {
		return errors.New("ServiceName is required")
	}

	unitPath, _, ctlArgs := derivePaths(Options{ServiceName: serviceName, UserMode: userMode})

	// disable --now <service>
	if out, err := run("systemctl", append(ctlArgs, "disable", "--now", serviceName)...); err != nil {
		// don't hard-fail on disable; print context
		_ = out
	}

	// remove file
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit file: %w", err)
	}

	// reload
	if out, err := run("systemctl", append(ctlArgs, "daemon-reload")...); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %v\n%s", err, out)
	}
	return nil
}

func Reload(serviceName string, userMode bool) error {
	_, _, ctlArgs := derivePaths(Options{ServiceName: serviceName, UserMode: userMode})
	if out, err := run("systemctl", append(ctlArgs, "daemon-reload")...); err != nil {
		return fmt.Errorf("daemon-reload failed: %v\n%s", err, out)
	}
	if out, err := run("systemctl", append(ctlArgs, "restart", serviceName)...); err != nil {
		return fmt.Errorf("restart %s failed: %v\n%s", serviceName, err, out)
	}
	return nil
}

func derivePaths(opts Options) (unitPath, wantedBy string, ctlArgs []string) {
	wantedBy = opts.WantedBy
	if opts.UserMode {
		if wantedBy == "" {
			wantedBy = "default.target"
		}
		if opts.UnitPath == "" {
			home, _ := os.UserHomeDir()
			opts.UnitPath = filepath.Join(home, ".config", "systemd", "user", opts.ServiceName+".service")
		}
		ctlArgs = []string{"--user"}
	} else {
		if wantedBy == "" {
			wantedBy = "multi-user.target"
		}
		if opts.UnitPath == "" {
			opts.UnitPath = filepath.Join("/etc", "systemd", "system", opts.ServiceName+".service")
		}
		ctlArgs = nil
	}
	return opts.UnitPath, wantedBy, ctlArgs
}

type unitData struct {
	Description string
	ExecPath    string
	ExecArgs    string
	Env         map[string]string
	WantedBy    string
	UserMode    bool
}

func renderUnit(d unitData) string {
	var envLines strings.Builder
	for k, v := range d.Env {
		// Quote complex values; systemd accepts shell-like quoting.
		envLines.WriteString(fmt.Sprintf("Environment=\"%s=%s\"\n", k, v))
	}

	return fmt.Sprintf(`[Unit]
Description=%s
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
%sExecStart=%s%s
Restart=on-failure
RestartSec=3
StandardOutput=journal
StandardError=journal
# In user units, don't set User/Group. In system units, prefer a dedicated user.
# ProtectHome/read-only may block reading configs in $HOME; enable only if safe.

[Install]
WantedBy=%s
`, d.Description, envLines.String(), d.ExecPath, d.ExecArgs, d.WantedBy)
}

func ensureSupported() error {
	if runtime.GOOS != "linux" {
		return errors.New("systemd is only supported on Linux")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl not found: %w", err)
	}
	return nil
}

func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
