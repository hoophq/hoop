package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/runopsio/hoop/common/clientconfig"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
)

var ErrEmpty error = errors.New("unable to locate configuration file")

const apiLocalhostURL = "http://127.0.0.1:8009"

type Config struct {
	Token    string `toml:"token"`
	ApiURL   string `toml:"api_url"`
	GrpcURL  string `toml:"grpc_url"`
	Mode     string `toml:"-"`
	filepath string `toml:"-"`
}

// Load builds an client config file in the following order.
// load the configuration based on environment variables  (HOOP_GRPCURL, HOOP_APIURL & HOOP_TOKEN)
// load based in the configuration file $HOME/.hoop/client.toml.
// load a configuration file if localhost grpc port has connectivity.
func Load() (*Config, error) {
	// TODO: if env is set, use it
	grpcURL := os.Getenv("HOOP_GRPCURL")
	apiServer := os.Getenv("HOOP_APIURL")
	accessToken := os.Getenv("HOOP_TOKEN")
	if grpcURL != "" && apiServer != "" && accessToken != "" {
		return &Config{
			Token:   accessToken,
			ApiURL:  apiServer,
			GrpcURL: grpcURL,
			Mode:    clientconfig.ModeEnv}, nil
	}

	// fallback to reading the configuration file
	filepath, err := clientconfig.NewPath(clientconfig.ClientFile)
	if err != nil {
		return nil, err
	}
	var conf Config
	if _, err := toml.DecodeFile(filepath, &conf); err != nil {
		return nil, fmt.Errorf("failed decoding configuration file=%v, err=%v", filepath, err)
	}

	if !conf.isEmpty() {
		conf.Mode = clientconfig.ModeConfigFile
		conf.filepath = filepath
		return &conf, nil
	}

	// fallback connecting to localhost without tls / authentication
	// if the gRPC localhost URL has connectivity
	timeout := time.Second * 5
	conn, err := net.DialTimeout("tcp", grpc.LocalhostAddr, timeout)
	if err == nil {
		conn.Close()
		return &Config{ApiURL: apiLocalhostURL, GrpcURL: grpc.LocalhostAddr, Mode: clientconfig.ModeLocal}, nil
	}
	return &Config{filepath: filepath}, ErrEmpty
}

// GrpcClientConfig returns a configuration to connect to the gRPC server
func (c *Config) GrpcClientConfig() (grpc.ClientConfig, error) {
	srvAddr, err := grpc.ParseServerAddress(c.GrpcURL)
	return grpc.ClientConfig{
		ServerAddress: srvAddr,
		TLSServerName: os.Getenv("TLS_SERVER_NAME"),
		Token:         c.Token,
		// connect without tls only on localhost
		Insecure: c.IsInsecure(),
	}, err
}

func (c *Config) isEmpty() bool { return c.GrpcURL == "" && c.ApiURL == "" }
func (c *Config) IsInsecure() (insecure bool) {
	switch {
	case os.Getenv("TLS_SERVER_NAME") != "":
	case c.Mode == clientconfig.ModeLocal:
		insecure = true
	}
	return
}
func (c *Config) IsValid() bool  { return c.GrpcURL != "" && c.ApiURL != "" }
func (c *Config) HasToken() bool { return c.Mode == clientconfig.ModeLocal || c.Token != "" }
func (c *Config) Save() error {
	if c.filepath == "" {
		return nil
	}
	debugTokenClaims(c.Token)
	confBuffer := bytes.NewBuffer([]byte{})
	if err := toml.NewEncoder(confBuffer).Encode(c); err != nil {
		return fmt.Errorf("failed saving config to %s, encode-err=%v", c.filepath, err)
	}
	if err := os.WriteFile(c.filepath, confBuffer.Bytes(), 0600); err != nil {
		return fmt.Errorf("failed saving config to %s, err=%v", c.filepath, err)
	}
	return nil
}

func debugTokenClaims(jwtToken string) {
	if len(strings.Split(jwtToken, ".")) != 3 {
		log.Debugf("jwt-token=false, length=%v", jwtToken)
		return
	}
	header, payload, found := strings.Cut(jwtToken, ".")
	if !found {
		log.Debugf("jwt-token=false, length=%v", jwtToken)
		return
	}
	headerBytes, _ := base64.StdEncoding.DecodeString(header)
	payloadBytes, _ := base64.StdEncoding.DecodeString(payload)
	headerBytes = bytes.ReplaceAll(headerBytes, []byte(`"`), []byte(`'`))
	payloadBytes = bytes.ReplaceAll(payloadBytes, []byte(`"`), []byte(`'`))
	log.Debugf("jwt-token=true, header=%v, payload=%v", string(headerBytes), string(payloadBytes))
}
