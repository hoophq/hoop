package clientconfig

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	AgentFile  string = "agent.toml"
	ClientFile string = "config.toml"
)

// NewHomeDir creates a home dir and any inner level folders passed in
// Passing any folder path will create only the default hoop home dir folder
func NewHomeDir(foldePaths ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed obtaing home dir, err=%v", err)
	}
	hoopHomeDirParts := append([]string{home, ".hoop"}, foldePaths...)
	hoopHomeDir := filepath.Join(hoopHomeDirParts...)
	if _, err := os.Stat(hoopHomeDir); os.IsNotExist(err) {
		if err := os.MkdirAll(hoopHomeDir, 0700); err != nil {
			return "", fmt.Errorf("failed creating hoop home dir (%s), err=%v", hoopHomeDir, err)
		}
	}
	return hoopHomeDir, nil
}

func NewPath(configFile string) (string, error) {
	hoopHomeDir, err := NewHomeDir()
	if err != nil {
		return "", err
	}
	filepath := fmt.Sprintf("%s/%s", hoopHomeDir, configFile)
	if _, err := os.Stat(filepath); os.IsNotExist(err) {
		f, err := os.Create(filepath)
		if err != nil {
			return filepath, fmt.Errorf("failed creating config file (%s), err=%v", filepath, err)
		}
		f.Close()
	}
	return filepath, nil
}
