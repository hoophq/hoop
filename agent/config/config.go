package config

import (
	"fmt"
	"os"

	"github.com/hoophq/hoop/common/clientconfig"
	"github.com/hoophq/hoop/common/dsnkeys"
	"github.com/hoophq/hoop/common/envloader"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
)

type Config struct {
	Token     string
	URL       string
	Name      string
	Type      string
	AgentMode string
	insecure  bool
	tlsCA     string
}

// Load the configuration based on environment variable HOOP_KEY or HOOP_DSN (legacy).
func Load() (*Config, error) {
	isLegacy, key := getEnvCredentials()
	dsn, err := dsnkeys.Parse(key)
	if err != nil && err != dsnkeys.ErrEmpty {
		if isLegacy {
			return nil, fmt.Errorf("HOOP_DSN (deprecated) is in wrong format, reason=%v", err)
		}
		return nil, fmt.Errorf("HOOP_KEY is in wrong format, reason=%v", err)
	}
	if dsn != nil {
		if isLegacy {
			log.Warnf("HOOP_DSN environment variable is deprecated, use HOOP_KEY instead")
		}
		tlsCA, err := envloader.GetEnv("HOOP_TLSCA")
		if err != nil {
			return nil, err
		}
		isInsecure := dsn.Scheme == "http" || dsn.Scheme == "grpc"
		return &Config{
			Name:      dsn.Name,
			Type:      clientconfig.ModeDsn,
			AgentMode: dsn.AgentMode,
			Token:     dsn.Key(),
			URL:       dsn.Address,
			insecure:  isInsecure,
			tlsCA:     tlsCA,
		}, nil
	}
	legacyToken := getLegacyHoopTokenCredentials()
	grpcURL := os.Getenv("HOOP_GRPCURL")
	if legacyToken != "" && grpcURL != "" {
		log.Warnf("HOOP_TOKEN and HOOP_GRPCURL environment variables are deprecated, create a new token to use the new format")
		return &Config{
			Type:      clientconfig.ModeEnv,
			AgentMode: proto.AgentModeStandardType,
			Token:     legacyToken,
			URL:       grpcURL,
			insecure:  grpcURL == grpc.LocalhostAddr}, nil
	}
	return nil, fmt.Errorf("missing HOOP_KEY environment variable")
}

func (c *Config) GrpcClientConfig() (grpc.ClientConfig, error) {
	srvAddr, err := grpc.ParseServerAddress(c.URL)
	return grpc.ClientConfig{
		ServerAddress: srvAddr,
		Token:         c.Token,
		Insecure:      c.IsInsecure(),
		TLSServerName: os.Getenv("HOOP_TLSSERVERNAME"),
		TLSCA:         c.tlsCA,
	}, err
}

func (c *Config) HasTlsCA() bool   { return c.tlsCA != "" }
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

func getLegacyHoopTokenCredentials() string {
	token := os.Getenv("TOKEN")
	if token != "" {
		return token
	}
	return os.Getenv("HOOP_TOKEN")
}
