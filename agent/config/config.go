package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/runopsio/hoop/agent/autoregister"
	"github.com/runopsio/hoop/common/clientconfig"
	"github.com/runopsio/hoop/common/dsnkeys"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/version"
)

type Config struct {
	Token     string `toml:"token"`
	URL       string `toml:"grpc_url"`
	Name      string `toml:"-"`
	Type      string `toml:"-"`
	AgentMode string `toml:"-"`
	IsV2      bool   `toml:"-"`
	filepath  string `toml:"-"`
	insecure  bool   `toml:"-"`
}

// Load builds an agent config file in the following order.
// load a configuration in auto register mode.
// load the configuration based on environment variable HOOP_DSN.
// load the configuration based on environment variables (HOOP_TOKEN & HOOP_GRPCURL).
// load based in the configuration file $HOME/.hoop/agent.toml.
func Load() (*Config, error) {
	agentToken, err := autoregister.Run()
	if err != nil {
		return nil, err
	}
	if agentToken != "" {
		return &Config{
			Type:      clientconfig.ModeAgentAutoRegister,
			AgentMode: proto.AgentModeStandardType,
			Token:     agentToken,
			URL:       grpc.LocalhostAddr,
			insecure:  true}, nil
	}
	dsn, err := dsnkeys.Parse(os.Getenv("HOOP_DSN"))
	if err != nil && err != dsnkeys.ErrEmpty {
		return nil, fmt.Errorf("HOOP_DSN in wrong format, reason=%v", err)
	}
	if dsn != nil {
		return &Config{
			Name:      dsn.Name,
			Type:      clientconfig.ModeDsn,
			AgentMode: dsn.AgentMode,
			Token:     dsn.Key(),
			URL:       dsn.Address,
			IsV2:      dsn.ApiV2,
			// allow connecting insecure if a build disables this flag
			insecure: !version.Get().StrictTLS && (dsn.Scheme == "http" || dsn.Scheme == "grpc")}, nil
	}

	// the above methods must be deprecated in the future in flavor of HOOP_DSN
	token := getEnvToken()
	grpcURL := os.Getenv("HOOP_GRPCURL")
	if token != "" && grpcURL != "" {
		return &Config{
			Type:      clientconfig.ModeEnv,
			AgentMode: proto.AgentModeStandardType,
			Token:     token,
			URL:       grpcURL,
			insecure:  grpcURL == grpc.LocalhostAddr}, nil
	}

	filepath, err := clientconfig.NewPath(clientconfig.AgentFile)
	if err != nil {
		return nil, err
	}
	var conf Config
	if _, err := toml.DecodeFile(filepath, &conf); err != nil {
		return nil, fmt.Errorf("failed decoding configuration file=%v, err=%v", filepath, err)
	}

	// always return if the configuration file isn't empty
	if !conf.isEmpty() {
		if !conf.IsValid() {
			return nil, fmt.Errorf("invalid configuration file, missing token or grpc url entries at %v", filepath)
		}
		conf.Type = clientconfig.ModeConfigFile
		conf.filepath = filepath
		conf.AgentMode = proto.AgentModeStandardType
		return &conf, nil
	}
	return nil, fmt.Errorf("missing HOOP_DSN environment variable")
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

func (c *Config) isEmpty() bool    { return c.URL == "" && c.Token == "" }
func (c *Config) IsInsecure() bool { return c.insecure }
func (c *Config) IsValid() bool    { return c.Token != "" && c.URL != "" }

// getEnvToken backwards compatible with TOKEN env
func getEnvToken() string {
	token := os.Getenv("TOKEN")
	if token != "" {
		return token
	}
	return os.Getenv("HOOP_TOKEN")
}
