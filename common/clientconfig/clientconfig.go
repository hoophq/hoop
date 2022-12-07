package clientconfig

import (
	"fmt"
	"os"
)

const (
	AgentFile  string = "agent.toml"
	ClientFile string = "config.toml"
)

func NewPath(configFile string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed obtaing home dir, err=%v", err)
	}
	hoopHomeDir := fmt.Sprintf("%s/.hoop", home)
	if _, err := os.Stat(hoopHomeDir); os.IsNotExist(err) {
		if err := os.MkdirAll(hoopHomeDir, 0700); err != nil {
			return "", fmt.Errorf("failed creating hoop home dir (%s), err=%v", hoopHomeDir, err)
		}
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
