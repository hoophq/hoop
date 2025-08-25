package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func userLaunchAgentPath(opts Options) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get user home dir: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", opts.ServiceName+".plist"), nil
}

func currentGuiTarget() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}


func ensureFile(path string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		return f.Close()
	}
	return nil
}

func fixedDarwinLogPaths(serviceName string) (stdoutPath, stderrPath string, _ error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("get user home dir: %w", err)
	}
	logDir := filepath.Join(home, "Library", "Logs")
	return filepath.Join(logDir, serviceName+".out.log"),
		filepath.Join(logDir, serviceName+".err.log"),
		nil
}
