package cmd

import (
	"log"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/briandowns/spinner"
	"github.com/runopsio/hoop/common/clientconfig"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
)

type (
	Config struct {
		Token string
		Host  string
		Port  string
	}
)

func loadConfig() *Config {
	path := getFilepath()
	var conf Config
	if _, err := toml.DecodeFile(path, &conf); err != nil {
		panic(err)
	}

	return &conf
}

func saveConfig(conf *Config) {
	f, err := os.OpenFile(getFilepath(), os.O_WRONLY, os.ModeAppend)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	if err := f.Truncate(0); err != nil {
		panic(err)
	}

	f.Seek(0, 0)

	if err := toml.NewEncoder(f).Encode(conf); err != nil {
		panic(err)
	}
}

func getFilepath() string {
	filepath, err := clientconfig.NewPath(clientconfig.ClientFile)
	if err != nil {
		panic(err)
	}
	return filepath
}

func getClientConfig() *Config {
	defaultHost := "127.0.0.1"
	defaultPort := "8010"

	config := loadConfig()

	if config.Host == "" {
		config.Host = defaultHost
	}

	if config.Port == "" {
		config.Port = defaultPort
	}

	if config.Host != "" &&
		!strings.HasPrefix(config.Host, defaultHost) &&
		config.Token == "" {
		if err := doLogin(nil); err != nil {
			panic(err)
		}
		config = loadConfig()
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

	client, err := grpc.Connect(
		config.Host+":"+config.Port,
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
