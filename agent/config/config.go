package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/agent/autoregister"
	"github.com/runopsio/hoop/common/clientconfig"
	"github.com/runopsio/hoop/common/grpc"
)

var errInvalid = errors.New("invalid configuration file content")

type Config struct {
	Token          string `toml:"token"`
	GrpcURL        string `toml:"grpc_url"`
	WebRegisterURL string `toml:"-"`
	Mode           string `toml:"-"`
	filepath       string `toml:"-"`
	saved          bool   `toml:"-"`
}

// Load builds an agent config file in the following order.
// load a configuration in auto register mode.
// load the configuration based on environment variables (HOOP_TOKEN & HOOP_GRPCURL)
// load based in the configuration file $HOME/.hoop/agent.toml.
// load a configuration file if localhost grpc port has connectivity.
// fallback to web registration in case none of the condition above matches.
func Load() (*Config, error) {
	agentToken, err := autoregister.Run()
	if err != nil {
		return nil, err
	}
	if agentToken != "" {
		return &Config{Mode: clientconfig.ModeAgentAutoRegister, Token: agentToken, GrpcURL: grpc.LocalhostAddr}, nil
	}
	token := os.Getenv("HOOP_TOKEN")
	grpcURL := os.Getenv("HOOP_GRPCURL")
	if token != "" && grpcURL != "" {
		return &Config{Mode: clientconfig.ModeEnv, Token: token, GrpcURL: grpcURL}, nil
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
		conf.Mode = clientconfig.ModeConfigFile
		conf.filepath = filepath
		return &conf, nil
	}
	// try connecting to localhost without tls / authentication
	// if the gRPC localhost URL has connectivity
	timeout := time.Second * 5
	conn, err := net.DialTimeout("tcp", grpc.LocalhostAddr, timeout)
	if err == nil {
		conn.Close()
		return &Config{Mode: clientconfig.ModeLocal, GrpcURL: grpc.LocalhostAddr, Token: ""}, nil
	}

	// fallback to web registration
	if grpcURL == "" {
		grpcURL = clientconfig.SaaSGrpcURL
	}
	conf = Config{
		Mode:     clientconfig.ModeAgentWebRegister,
		GrpcURL:  grpcURL,
		Token:    "x-agt-" + uuid.NewString(),
		filepath: filepath,
	}

	switch conf.GrpcURL {
	case "", clientconfig.SaaSGrpcURL:
		conf.WebRegisterURL = fmt.Sprintf("%s/agents/new/%s", clientconfig.SaaSWebURL, conf.Token)
	default:
		// self-hosted
		conf.WebRegisterURL = fmt.Sprintf("{API_URL}/agents/new/%s", conf.Token)
	}
	return &conf, nil
}

func (c *Config) GrpcClientConfig() (grpc.ClientConfig, error) {
	srvAddr, err := grpc.ParseServerAddress(c.GrpcURL)
	return grpc.ClientConfig{
		ServerAddress: srvAddr,
		TLSServerName: os.Getenv("TLS_SERVER_NAME"),
		Token:         c.Token,
		Insecure:      c.IsInsecure(),
	}, err
}

func (c *Config) isEmpty() bool { return c.GrpcURL == "" && c.Token == "" }
func (c *Config) IsInsecure() (insecure bool) {
	switch {
	case os.Getenv("TLS_SERVER_NAME") != "":
	case c.Mode == clientconfig.ModeLocal, c.Mode == clientconfig.ModeAgentAutoRegister:
		insecure = true
	}
	return
}
func (c *Config) IsValid() bool { return c.Token != "" && c.GrpcURL != "" }
func (c *Config) IsSaved() bool { return c.saved }
func (c *Config) Delete()       { _ = os.Remove(c.filepath) }
func (c *Config) Save() error {
	if c.Mode != clientconfig.ModeAgentWebRegister {
		return nil
	}
	confBuffer := bytes.NewBuffer([]byte{})
	if err := toml.NewEncoder(confBuffer).Encode(c); err != nil {
		return fmt.Errorf("failed saving config to %s, encode-err=%v", c.filepath, err)
	}
	if err := os.WriteFile(c.filepath, confBuffer.Bytes(), 0600); err != nil {
		return fmt.Errorf("failed saving config to %s, err=%v", c.filepath, err)
	}
	c.saved = true
	return nil
}
