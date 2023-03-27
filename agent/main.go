package agent

import (
	"fmt"
	"io"
	"time"

	agentconfig "github.com/runopsio/hoop/agent/config"
	"github.com/runopsio/hoop/common/clientconfig"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/version"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func Run() {
	config, err := agentconfig.Load()
	if err != nil {
		log.Fatal(err)
	}

	clientOptions := grpc.WithOption("origin", pb.ConnectionOriginAgent)
	clientConfig, err := config.GrpcClientConfig()
	if err != nil {
		log.Fatal(err)
	}
	vs := version.Get()
	log.Infof("version=%v, platform=%v, mode=%v, grpc_server=%v, tls=%v - starting agent",
		vs.Version, vs.Platform, config.Mode, config.GrpcURL, config.IsSecure())
	switch config.Mode {
	case clientconfig.ModeAgentWebRegister:
		for i := 0; ; i++ {
			log.Infof("webregister - connecting, attempt=%v", i+1)
			client, err := grpc.Connect(clientConfig, clientOptions)
			if err != nil {
				log.Fatalf("failed to connect to %s, err=%v", config.GrpcURL, err.Error())
			}
			err = New(client, config).Run()
			if config.IsSaved() && err != nil {
				log.Warnf("disconnected from %v, err=%v", config.GrpcURL, err)
				break
			}
			if e, ok := status.FromError(err); ok && e.Code() == codes.Unauthenticated {
				if i == 0 {
					fmt.Print("\n--------------------------------------------------------------------------\n")
					fmt.Println("VISIT THE URL BELOW TO REGISTER THE AGENT")
					fmt.Print(config.WebRegisterURL)
					fmt.Print("\n--------------------------------------------------------------------------\n")
					fmt.Println()
				}
				if i >= 30 { // ~3 minutes
					log.Warnf("timeout on registering the agent")
					break
				}
				time.Sleep(time.Second * 7)
			}
		}
	default:
		client, err := grpc.Connect(clientConfig, clientOptions)
		if err != nil {
			log.Fatalf("failed to connect to %s, err=%v", config.GrpcURL, err.Error())
		}
		err = New(client, config).Run()
		if err != io.EOF {
			log.Fatalf("disconnected from %v, err=%v", config.GrpcURL, err.Error())
		}
		log.Warnf("disconnected from %v", config.GrpcURL)
	}
}
