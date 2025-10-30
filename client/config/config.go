package clientconfig

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
	"github.com/google/uuid"
	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/hoophq/hoop/common/clientconfig"
	"github.com/hoophq/hoop/common/envloader"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
)

var ErrEmpty error = errors.New("unable to locate configuration file")

const apiLocalhostURL = "http://127.0.0.1:8009"

type Config struct {
	Token         string `toml:"token"`
	ApiURL        string `toml:"api_url"`
	GrpcURL       string `toml:"grpc_url"`
	TlsCAB64Enc   string `toml:"tls_ca"`
	SkipTLSVerify bool   `toml:"skip_tls_verify"`
	Mode          string `toml:"-"`
	InsecureGRPC  bool   `toml:"-"`
	filepath      string `toml:"-"`
}

// NewConfigFile creates a new configuration in the filesystem
func NewConfigFile(apiURL, grpcURL, token, tlsCA string) (string, error) {
	filepath, err := clientconfig.NewPath(clientconfig.ClientFile)
	if err != nil {
		return "", err
	}
	config := &Config{
		filepath:      filepath,
		Token:         token,
		ApiURL:        apiURL,
		GrpcURL:       grpcURL,
		SkipTLSVerify: false,
		TlsCAB64Enc:   base64.StdEncoding.EncodeToString([]byte(tlsCA)),
	}
	_, err = config.Save()
	return filepath, err
}

// Remove the configuration file if it exists
func Remove() error {
	filepath, _ := clientconfig.NewPath(clientconfig.ClientFile)
	if filepath != "" {
		return os.Remove(filepath)
	}
	return nil
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
	skipTLSVerify := os.Getenv("HOOP_TLS_SKIP_VERIFY") == "true"
	tlsCA, err := envloader.GetEnv("HOOP_TLSCA")
	if err != nil {
		return nil, err
	}
	if grpcURL != "" && apiServer != "" && accessToken != "" {
		return &Config{
			Token:         accessToken,
			ApiURL:        apiServer,
			GrpcURL:       grpcURL,
			SkipTLSVerify: skipTLSVerify,
			TlsCAB64Enc:   base64.StdEncoding.EncodeToString([]byte(tlsCA)),
			Mode:          clientconfig.ModeEnv,
			InsecureGRPC:  hasInsecureScheme(grpcURL)}, nil
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
		if conf.TlsCAB64Enc == "" {
			conf.TlsCAB64Enc = base64.StdEncoding.EncodeToString([]byte(tlsCA))
		}
		conf.Mode = clientconfig.ModeConfigFile
		conf.filepath = filepath
		conf.InsecureGRPC = hasInsecureScheme(conf.GrpcURL)
		conf.SkipTLSVerify = skipTLSVerify
		return &conf, nil
	}

	// fallback connecting to localhost without tls / authentication
	// if the gRPC localhost URL has connectivity
	timeout := time.Second * 5
	conn, err := net.DialTimeout("tcp", grpc.LocalhostAddr, timeout)
	if err == nil {
		conn.Close()
		return &Config{
			ApiURL:       apiLocalhostURL,
			GrpcURL:      grpc.LocalhostAddr,
			Mode:         clientconfig.ModeLocal,
			Token:        accessToken,
			InsecureGRPC: true,
		}, nil
	}
	return &Config{filepath: filepath}, ErrEmpty
}

// GrpcClientConfig returns a configuration to connect to the gRPC server
func (c *Config) GrpcClientConfig() (grpc.ClientConfig, error) {
	srvAddr, isTLS, err := grpc.ParseServerAddress(c.GrpcURL)
	return grpc.ClientConfig{
		ServerAddress: srvAddr,
		Token:         c.Token,
		Insecure:      c.InsecureGRPC,
		IsTLS:         isTLS,
		TLSSkipVerify: c.SkipTLSVerify,
		TLSServerName: os.Getenv("HOOP_TLSSERVERNAME"),
		TLSCA:         c.TlsCA(),
	}, err
}

func (c *Config) isEmpty() bool  { return c.GrpcURL == "" && c.ApiURL == "" }
func (c *Config) IsValid() bool  { return c.ApiURL != "" }
func (c *Config) HasToken() bool { return c.Mode == clientconfig.ModeLocal || c.Token != "" }
func (c *Config) IsApiKey() bool {
	if strings.HasPrefix(c.Token, "xapi-") {
		return true
	}
	// legacy api key format
	parts := strings.Split(c.Token, "|")
	if _, err := uuid.Parse(parts[0]); err == nil {
		return true
	}
	return false
}
func (c *Config) TlsCA() string {
	if c.TlsCAB64Enc != "" {
		tlsCA, _ := base64.StdEncoding.DecodeString(c.TlsCAB64Enc)
		return string(tlsCA)
	}
	return ""
}

func (c *Config) Save() (bool, error) {
	if c.filepath == "" {
		return false, nil
	}
	debugTokenClaims(c.Token)
	confBuffer := bytes.NewBuffer([]byte{})
	if err := toml.NewEncoder(confBuffer).Encode(c); err != nil {
		return false, fmt.Errorf("failed saving config to %s, encode-err=%v", c.filepath, err)
	}
	if err := os.WriteFile(c.filepath, confBuffer.Bytes(), 0600); err != nil {
		return false, fmt.Errorf("failed saving config to %s, err=%v", c.filepath, err)
	}
	return true, nil
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

func GetClientConfigOrDie() *Config {
	config, err := Load()
	switch err {
	case ErrEmpty, nil:
	default:
		styles.PrintErrorAndExit(err.Error())
	}
	log.Debugf("loaded clientconfig, mode=%v, grpc-tls=%v, api_url=%v, grpc_url=%v, isapikey=%v, tokenlength=%v, tlsca=%v",
		config.Mode, !config.InsecureGRPC, config.ApiURL, config.GrpcURL, config.IsApiKey(), len(config.Token), config.TlsCAB64Enc != "")
	return config
}

func GetClientConfig() (*Config, error) {
	config, err := Load()
	switch err {
	case ErrEmpty, nil:
		if !config.IsValid() || !config.HasToken() {
			return nil, fmt.Errorf("unable to load credentials, configuration invalid or missing token")
		}
	default:
		return nil, err
	}
	log.Infof("loaded clientconfig, mode=%v, grpc-tls=%v, api_url=%v, grpc_url=%v, tlsca=%v",
		config.Mode, !config.InsecureGRPC, config.ApiURL, config.GrpcURL, config.TlsCAB64Enc != "")
	return config, nil
}

// hasInsecureScheme returns true if the address has an insecure scheme (grpc:// or http://)
func hasInsecureScheme(srvAddr string) bool {
	return strings.HasPrefix(srvAddr, "grpc://") ||
		strings.HasPrefix(srvAddr, "http://")
}
