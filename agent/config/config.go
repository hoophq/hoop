package config

import (
	"fmt"
	"os"

	"github.com/runopsio/hoop/common/clientconfig"
	"github.com/runopsio/hoop/common/dsnkeys"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/version"
)

type Config struct {
	Token     string
	URL       string
	Name      string
	Type      string
	AgentMode string
	insecure  bool
}

// Load the configuration based on environment variable HOOP_KEY or HOOP_DSN (legacy).
func Load() (*Config, error) {
	isLegacy, key := getEnvCredentials()
	dsn, err := dsnkeys.Parse(key)
	if err != nil && err != dsnkeys.ErrEmpty {
		if isLegacy {
			return nil, fmt.Errorf("HOOP_DSN (deprecated) in wrong format, reason=%v", err)
		}
		return nil, fmt.Errorf("HOOP_KEY in wrong format, reason=%v", err)
	}
	return &Config{
		Name:      dsn.Name,
		Type:      clientconfig.ModeDsn,
		AgentMode: dsn.AgentMode,
		Token:     dsn.Key(),
		URL:       dsn.Address,
		// allow connecting insecure if a build disables this flag
		insecure: !version.Get().StrictTLS && (dsn.Scheme == "http" || dsn.Scheme == "grpc")}, nil
}

func (c *Config) GrpcClientConfig() (grpc.ClientConfig, error) {
	srvAddr, err := grpc.ParseServerAddress(c.URL)
	return grpc.ClientConfig{
		ServerAddress: srvAddr,
		TLSServerName: os.Getenv("TLS_SERVER_NAME"),
		Token:         c.Token,
		Insecure:      c.IsInsecure(),
	}, err
}

func (c *Config) IsInsecure() bool { return c.insecure }
func (c *Config) IsValid() bool    { return c.Token != "" && c.URL != "" }

// getEnvToken backwards compatible with HOOP_DSN env
func getEnvCredentials() (legacy bool, v string) {
	v = os.Getenv("HOOP_KEY")
	if v != "" {
		return
	}
	return true, os.Getenv("HOOP_DSN")
}
