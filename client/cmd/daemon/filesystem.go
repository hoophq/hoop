package daemon

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentconfig "github.com/hoophq/hoop/agent/config"
	"github.com/hoophq/hoop/common/log"
)

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

func envFileAlreadyExist(path string) bool {
	if _, err := os.Stat(path); err == nil {
		return true
	}
	return false
}

func envFileExist() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	envPath := filepath.Join(home, ".config", "hoop.conf")
	if envFileAlreadyExist(envPath) {
		return envPath, nil
	}
	return "", fmt.Errorf("env file not found")
}

func LoadEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	env := make(map[string]string)
	scanner := bufio.NewScanner(f)
	home, _ := os.UserHomeDir()

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue // skip malformed line
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		if strings.HasPrefix(val, "~/") {
			val = filepath.Join(home, val[2:])
		}

		env[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return env, nil
}

func createEnvFileIfNotExists(env map[string]string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Errorf("error getting user home dir: %v", err)
		return "", err
	}
	envDir := filepath.Join(home, ".config")
	envPath := filepath.Join(envDir, "hoop.conf")

	if err := os.MkdirAll(envDir, 0o755); err != nil {
		log.Errorf("error creating config dir: %v", err)
		return "", err
	}

	content := ""
	for k, v := range env {
		content += fmt.Sprintf("%s=%s\n", k, v)
	}

	if err := writeFileIfNotExists(envPath, content, 0o600); err != nil {
		log.Errorf("error writing env file: %v", err)
		return "", err
	}
	return envPath, nil
}

func writeFileIfNotExists(path, content string, perm os.FileMode) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte(content), perm); err != nil {
			log.Errorf("Error writing the file %s: %v", path, err)
			return err
		}
	} else if err != nil {
		log.Errorf("Error stat file %s: %v", path, err)
		return err
	}
	return nil
}

func configEnvironmentVariables() (map[string]string, error) {
	envFile, err := envFileExist()
	if envFile != "" {
		log.Infof("Using existing env file: %s", envFile)
		env, err := LoadEnvFile(envFile)
		return env, err
	}

	cfg, err := agentconfig.Load()
	if err != nil {
		return nil, err
	}

	envKeys := map[string]string{
		"HOOP_KEY": cfg.Token,
		"PATH":     os.Getenv("PATH"),
	}

	envFile, err = createEnvFileIfNotExists(envKeys)
	if err != nil {
		log.Errorf("error creating env file: %w", err)
		return nil, err
	}

	return LoadEnvFile(envFile)
}
