package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentconfig "github.com/hoophq/hoop/agent/config"
)

func StartDarwinAgent() error {
	cfg, err := agentconfig.Load()

	if err != nil {
		return err
	}

	opts := Options{
		ServiceName: "hoop-agent",
		ExecArgs:    " start agent",
		Env: map[string]string{
			"HOOP_KEY": cfg.Token,
		},
	}
	if err := installDarwin(opts); err != nil {
		return err
	}
	return nil
}

func installDarwin(opts Options) error {
	exe, err := execPath()
	if err != nil {
		return err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return fmt.Errorf("resolve executable symlinks: %w", err)
	}

	exe = strings.ReplaceAll(exe, "%", "%%")

	plistPath, err := userLaunchAgentPath(opts)
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get user home dir: %w", err)
	}

	stdOut := filepath.Join(home, "Library", "Logs", opts.ServiceName+".out.log")
	stdErr := filepath.Join(home, "Library", "Logs", opts.ServiceName+".err.log")

	plist := renderLaunchAgentPlist(launchAgentData{
		Label:                 opts.ServiceName,
		Program:               exe,
		ProgramArgumentsExtra: splitArgs(opts.ExecArgs),
		EnvironmentVariables:  opts.Env,
		RunAtLoad:             true,
		KeepAlive:             true,
		StandardOutPath:       stdOut,
		StandardErrorPath:     stdErr,
	})

	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("mkdir LaunchAgents dir: %w", err)
	}
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	guiTarget := currentGuiTarget()
	if _, err := execRunner.Run("launchctl", "bootstrap", guiTarget, plistPath); err != nil {
		return err
	}

	if out, err := execRunner.Run("launchctl", "enable", guiTarget+"/"+opts.ServiceName); err != nil {
		return fmt.Errorf("launchctl enable %s failed: %v\n%s", opts.ServiceName, err, out)
	}

	if out, err := execRunner.Run("launchctl", "kickstart", "-k", guiTarget+"/"+opts.ServiceName); err != nil {
		return fmt.Errorf("launchctl kickstart %s failed: %v\n%s", opts.ServiceName, err, out)
	}

	return nil
}

func splitArgs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Fields(s)
	return parts
}


func renderLaunchAgentPlist(d launchAgentData) string {
	var progArgs strings.Builder
	// ProgramArguments: [Program, ...ExecArgs...]
	progArgs.WriteString("\t<array>\n")
	progArgs.WriteString(fmt.Sprintf("\t\t<string>%s</string>\n", xmlEscape(d.Program)))
	for _, a := range d.ProgramArgumentsExtra {
		progArgs.WriteString(fmt.Sprintf("\t\t<string>%s</string>\n", xmlEscape(a)))
	}
	progArgs.WriteString("\t</array>\n")

	var env strings.Builder
	if len(d.EnvironmentVariables) > 0 {
		env.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		for k, v := range d.EnvironmentVariables {
			env.WriteString(fmt.Sprintf("\t\t<key>%s</key>\n\t\t<string>%s</string>\n", xmlEscape(k), xmlEscape(v)))
		}
		env.WriteString("\t</dict>\n")
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
%s	<key>RunAtLoad</key>
	<%s/>
	<key>KeepAlive</key>
	<%s/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
%s</dict>
</plist>
`, xmlEscape(d.Label),
		progArgs.String(),
		boolToXML(d.RunAtLoad),
		boolToXML(d.KeepAlive),
		xmlEscape(d.StandardOutPath),
		xmlEscape(d.StandardErrorPath),
		env.String(),
	)
}

func boolToXML(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func xmlEscape(s string) string {
	// minimal escaping for plist/XML
	r := strings.NewReplacer(
		`&`, "&amp;",
		`<`, "&lt;",
		`>`, "&gt;",
		`"`, "&quot;",
		`'`, "&apos;",
	)
	return r.Replace(s)
}

func StopDarwinAgent() error {
	if err := stopDarwin("hoop-agent"); err != nil {
		return fmt.Errorf("failed to stop hoop-agent: %w", err)
	}
	return nil
}

func stopDarwin(serviceName string) error {
	guiTarget := currentGuiTarget()
	if _, err := execRunner.Run("launchctl", "bootout", guiTarget+"/"+serviceName); err != nil {
		return err
	}
	return nil
}

func RemoveDarwinAgent() error {
	if err := removeDarwin("hoop-agent"); err != nil {
		return err
	}
	return nil
}

func removeDarwin(serviceName string) error {
	_ = stopDarwin(serviceName)

	plistPath, err := userLaunchAgentPath(Options{ServiceName: serviceName})
	if err != nil {
		return err
	}

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}

func LogsDarwinAgent() error {
	return logsAgentDarwin("hoop-agent")
}

func logsAgentDarwin(serviceName string) error {
	stdout, stderr, err := fixedDarwinLogPaths(serviceName)
	if err != nil {
		return err
	}

	if err := ensureFile(stdout); err != nil {
		return fmt.Errorf("prepare stdout log: %w", err)
	}
	if stderr != stdout {
		if err := ensureFile(stderr); err != nil {
			return fmt.Errorf("prepare stderr log: %w", err)
		}
	}

	args := []string{"-n", "+1", "-F", stdout}
	if stderr != stdout {
		args = append(args, stderr)
	}
	return execRunner.Logs("tail", args...)
}


