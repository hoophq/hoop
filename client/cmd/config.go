package cmd

import (
	"github.com/briandowns/spinner"
	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
)

func getClientConfigOrDie() *clientconfig.Config {
	config, err := clientconfig.Load()
	switch err {
	case clientconfig.ErrEmpty, nil:
		if !config.IsValid() || !config.HasToken() {
			printErrorAndExit("unable to load credentials, run 'hoop login' to configure it")
		}
	default:
		printErrorAndExit(err.Error())
	}
	log.Debugf("loaded clientconfig, mode=%v, tls=%v, api_url=%v, grpc_url=%v, tokenlength=%v",
		config.Mode, !config.IsInsecure(), config.ApiURL, config.GrpcURL, len(config.Token))
	return config
}

func newClientConnect(config *clientconfig.Config, loader *spinner.Spinner, args []string, verb string) *connect {
	c := &connect{
		proxyPort:      connectFlags.proxyPort,
		connStore:      memory.New(),
		clientArgs:     args[1:],
		connectionName: args[0],
		loader:         loader,
	}
	grpcClientOptions := []*grpc.ClientOptions{
		grpc.WithOption(grpc.OptionConnectionName, c.connectionName),
		grpc.WithOption("origin", pb.ConnectionOriginClient),
		grpc.WithOption("verb", verb),
	}
	clientConfig, err := config.GrpcClientConfig()
	if err != nil {
		c.printErrorAndExit(err.Error())
	}
	c.client, err = grpc.Connect(clientConfig, grpcClientOptions...)
	if err != nil {
		c.printErrorAndExit(err.Error())
	}
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
