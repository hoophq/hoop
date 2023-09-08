package clientconfig

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	AgentFile          string = "agent.toml"
	ClientFile         string = "config.toml"
	clientConfigFolder string = ".hoop"

	SaaSWebURL  string = "https://app.hoop.dev"
	SaaSGrpcURL string = "app.hoop.dev:8443"

	// ModeEnv is when a client is loaded with environment variables
	ModeEnv = "env"
	// ModeEnv is when a client is loaded with the environment variable HOOP_DSN
	ModeDsn = "dsn"
	// ModeLocal detects if the client has found a local instance
	// of the hoop gateway, this mode indicates the gRPC connection
	// should be established without tls encryption. For security
	// sake, only the localhost address should receive insecure connections
	ModeLocal = "local"
	// ModeConfigFile indicates the configuration is obtained from a configuration file
	// located in the filesystem.
	ModeConfigFile = "configfile"
	// ModeAgentAutoRegister should autoregister an agent connecting locally.
	// It is useful to start a local agent that grants access to the internal
	// network of the gateway. It allows creating connections to perform
	// administrative tasks.
	ModeAgentAutoRegister = "autoregister"
)

// NewHomeDir creates a home dir and any inner level folders passed in
// Passing any folder path will create only the default hoop home dir folder
func NewHomeDir(folderPaths ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed obtaing home dir, err=%v", err)
	}
	hoopHomeDirParts := append([]string{home, clientConfigFolder}, folderPaths...)
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
		_ = f.Chmod(0600)
		_ = f.Close()
	}
	return filepath, nil
}
