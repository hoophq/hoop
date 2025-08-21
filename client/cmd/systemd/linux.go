package systemd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentconfig "github.com/hoophq/hoop/agent/config"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
)

var vi = version.Get()

type Options struct {
	ServiceName string
	ExecArgs    string
	Env         map[string]string
	UnitPath    string
	WantedBy    string
}

func execPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get executable path: %w", err)
	}

	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolve executable symlinks: %w", err)
	}
	return exe, nil
}

func LogsLinuxAgent() error {
	logsAgent("hoop-agent")
	return nil
}

func logsAgent(serviceName string) error {
	args := []string{"--user", "-u", serviceName + ".service", "-f", "-o", "cat", "--no-pager"}
	if err := execRunner.Logs("journalctl", args...); err != nil {
		return fmt.Errorf("journalctl --user -u %s failed: %v\n", serviceName, err)
	}
	return nil
}
func StartLinuxAgent() error {
	cfg, err := agentconfig.Load()

	if err != nil {
		log.With("version", vi.Version).Fatal(err)
	}

	opts := Options{
		ServiceName: "hoop-agent",
		ExecArgs:    " start agent",
		Env: map[string]string{
			"HOOP_KEY": cfg.Token,
		},
		WantedBy: "default.target",
	}

	if err := install(opts); err != nil {
		return err
	}

	return nil
}

func install(opts Options) error {
	exe, err := execPath()
	if err != nil {
		return err
	}

	exe, err = filepath.Abs(exe)
	if err != nil {
		return fmt.Errorf("resolve executable symlinks: %w", err)
	}

	exe = strings.ReplaceAll(exe, "%", "%%")
	unitPath := userPaths(opts)

	unit := renderServiceFile(
		unitData{
			Description: fmt.Sprintf("%s Service", opts.ServiceName),
			ExecPath:    exe,
			ExecArgs:    opts.ExecArgs,
			Env:         opts.Env,
			WantedBy:    opts.WantedBy,
		})

	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("mkdir unit dir: %w", err)
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}

	if out, err := execRunner.Run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %v\n%s", err, out)
	}

	if out, err := execRunner.Run("systemctl", "--user", "enable", "--now", opts.ServiceName); err != nil {
		return fmt.Errorf("systemctl enable --now %s failed: %v\n%s", opts.ServiceName, err, out)
	}

	return nil
}

func StopLinuxAgent() error {
	if err := stop("hoop-agent"); err != nil {
		return fmt.Errorf("failed to stop hoop-agent: %w", err)
	}
	return nil
}

func stop(serviceName string) error {
	if out, err := execRunner.Run("systemctl", "--user", "stop", serviceName); err != nil {
		return fmt.Errorf("systemctl stop %s failed: %v\n%s", serviceName, err, out)
	}

	if out, err := execRunner.Run("systemctl", "--user", "disable", serviceName); err != nil {
		return fmt.Errorf("systemctl disable %s failed: %v\n%s", serviceName, err, out)
	}

	return nil
}

func RemoveLinuxAgent() error {
	if err := remove("hoop-agent"); err != nil {
		return err
	}
	return nil
}

func remove(serviceName string) error {
	unitPath := userPaths(Options{ServiceName: serviceName})

	if out, err := execRunner.Run("systemctl", "--user", "disable", "--now", serviceName); err != nil {
		_ = out
	}

	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit file: %w", err)
	}

	if out, err := execRunner.Run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %v\n%s", err, out)
	}
	return nil
}

func userPaths(opts Options) (unitPath string) {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", opts.ServiceName+".service")
}

type unitData struct {
	Description string
	ExecPath    string
	ExecArgs    string
	Env         map[string]string
	WantedBy    string
}

func renderServiceFile(d unitData) string {
	var envLines strings.Builder
	for k, v := range d.Env {
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
