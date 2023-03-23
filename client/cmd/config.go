package cmd

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/runopsio/hoop/common/log"

	"github.com/BurntSushi/toml"
	"github.com/briandowns/spinner"
	"github.com/runopsio/hoop/common/clientconfig"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
)

const (
	defaultApiURL  = "https://app.hoop.dev"
	defaultGrpcURL = "https://app.hoop.dev:8443"

	localApiURL  = "http://127.0.0.1:8009"
	localGrpcURL = "http://127.0.0.1:8010"
)

type Config struct {
	Token          string `toml:"access_token"`
	ApiURL         string `toml:"api_url"`
	GrpcURL        string `toml:"grpc_url"`
	ConfigFilePath string `toml:"-"`
}

func (c *Config) Validate() error {
	if _, err := url.Parse(c.ApiURL); err != nil {
		return fmt.Errorf("API_URL in wrong format, err=%v", err)
	}
	if _, err := url.Parse(c.GrpcURL); err != nil {
		return fmt.Errorf("GRPC_URL in wrong format, err=%v", err)
	}
	return nil
}

// parse into host:port
func (c *Config) grpcAddress() string {
	u, _ := url.Parse(c.GrpcURL)
	if u != nil {
		return fmt.Sprintf("%s:%s", u.Hostname(), u.Port())
	}
	return ""
}

func loadConfig() (*Config, error) {
	filepath, err := clientconfig.NewPath(clientconfig.ClientFile)
	if err != nil {
		return nil, fmt.Errorf("failed getting config file path, err=%v", err)
	}
	var conf Config
	if _, err := toml.DecodeFile(filepath, &conf); err != nil {
		return nil, fmt.Errorf("failed decoding toml config, err=%v", err)
	}
	conf.ConfigFilePath = filepath
	return &conf, nil
}

func saveConfig(conf *Config) error {
	f, err := os.OpenFile(conf.ConfigFilePath, os.O_WRONLY, os.ModeAppend)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Truncate(0); err != nil {
		return err
	}
	f.Seek(0, 0)
	return toml.NewEncoder(f).Encode(conf)
}

// getClientConfigOrDie will load the configuration file from the filesystem.
// If the configuration file doesn't exists, fallback to the localhost grpc server.
// If the localhost grpc doesn't respond, fallback performing the signin to the defaultApiURL
// saving as the default configuration.
func getClientConfigOrDie() *Config {
	config, err := loadConfig()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	if config.ApiURL == "" || config.GrpcURL == "" {
		// try connecting locally
		timeout := time.Second * 5
		u, _ := url.Parse(localGrpcURL)
		conn, err := net.DialTimeout("tcp", u.Host, timeout)
		if err == nil {
			conn.Close()
			config.ApiURL = localApiURL
			config.GrpcURL = localGrpcURL
			return config
		}
		// fallback to signin to defaultApiURL
		token, err := doLogin(defaultApiURL)
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		config.Token = token
		config.ApiURL = defaultApiURL
		config.GrpcURL = defaultGrpcURL
		if err := saveConfig(config); err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
	}
	return config
}

func newClientConnect(config *Config, loader *spinner.Spinner, args []string, verb string) *connect {
	c := &connect{
		proxyPort:      connectFlags.proxyPort,
		connStore:      memory.New(),
		clientArgs:     args[1:],
		connectionName: args[0],
		loader:         loader,
	}
	if err := config.Validate(); err != nil {
		c.printErrorAndExit(err.Error())
	}
	client, err := grpc.Connect(
		config.grpcAddress(),
		config.Token,
		grpc.WithOption(grpc.OptionConnectionName, c.connectionName),
		grpc.WithOption("origin", pb.ConnectionOriginClient),
		grpc.WithOption("verb", verb))
	if err != nil {
		c.printErrorAndExit(err.Error())
	}

	c.client = client
	return c
}

func newClientArgsSpec(clientArgs []string) map[string][]byte {
	spec := map[string][]byte{}
	if len(clientArgs) > 0 {
		encArgs, err := pb.GobEncode(clientArgs)
		if err != nil {
			log.Fatalf("failed encoding args, err=%v", err)
		}
		spec[pb.SpecClientExecArgsKey] = encArgs
	}
	return spec
}
