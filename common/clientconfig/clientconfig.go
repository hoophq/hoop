package clientconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	AgentFile          string = "agent.toml"
	ClientFile         string = "config.toml"
	clientConfigFolder string = ".hoop"

	// ConfigFileEnv selects the client config file for this process.
	// When it is unset, the file is $HOME/.hoop/config.toml.
	ConfigFileEnv = "HOOP_CONFIG"

	SaaSWebURL  string = "https://use.hoop.dev"
	SaaSGrpcURL string = "use.hoop.dev:8443"

	// ModeEnv is when a client is loaded with environment variables
	ModeEnv = "env"
	// ModeEnv is when a client is loaded with the environment variable HOOP_KEY
	ModeDsn = "dsn"
	// ModeSVID is when an agent is loaded with the environment variable
	// HOOP_SPIFFE_KEY_FILE pointing at a file that contains a bare JWT-SVID.
	// The file is re-read on every reconnect so rotated tokens are picked up.
	ModeSVID = "svid"
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

// newHomeDir creates a home dir and any inner level folders passed in
// Passing any folder path will create only the default hoop home dir folder
func newHomeDir(folderPaths ...string) (string, error) {
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

// configFileOverride returns the HOOP_CONFIG path.
// An empty path means the variable is unset.
func configFileOverride() (string, error) {
	raw := strings.TrimSpace(os.Getenv(ConfigFileEnv))
	if raw == "" {
		return "", nil
	}
	if raw == "~" {
		return "", fmt.Errorf("HOOP_CONFIG must be a file path")
	}
	if len(raw) > 1 && raw[0] == '~' && (raw[1] == '/' || raw[1] == '\\') {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed expanding HOOP_CONFIG, err=%w", err)
		}
		raw = filepath.Join(home, raw[2:])
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("failed resolving HOOP_CONFIG, err=%w", err)
	}
	return abs, nil
}

func NewPath(configFile string) (filePath, hoopHomeDir string, err error) {
	if configFile == ClientFile {
		filePath, err = configFileOverride()
		if err != nil {
			return "", "", err
		}
		// Do not create an override file. A missing path must fail at load.
		// Do not create $HOME/.hoop when the override is a complete path.
		if filePath != "" {
			if home, homeErr := os.UserHomeDir(); homeErr == nil {
				hoopHomeDir = filepath.Join(home, clientConfigFolder)
			}
			return filePath, hoopHomeDir, nil
		}
	}
	hoopHomeDir, err = newHomeDir()
	if err != nil {
		return "", "", err
	}
	filePath = filepath.Join(hoopHomeDir, configFile)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		f, err := os.Create(filePath)
		if err != nil {
			return filePath, hoopHomeDir, fmt.Errorf("failed creating config file (%s), err=%v", filePath, err)
		}
		_ = f.Chmod(0600)
		_ = f.Close()
	}
	return
}
